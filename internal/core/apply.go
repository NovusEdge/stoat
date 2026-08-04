package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/sshx"
)

// ErrAppliedAtBoot is returned by Apply for a VM whose backend already ran
// its recipes at first boot and has no post-boot apply step at all: the
// cloudinit backend, whose recipes are merged into the cloud-init seed and
// consumed by the guest's own cloud-init service before sshd is even
// reachable from stoat's side (internal/backend/cloudinit.go's Prepare;
// there is no matching Provision). Calling sshx.Provision on such a VM would
// not be a harmless no-op: v.Recipes holds "*.cloud.yaml" fragment names,
// and Provision pipes whatever it's given to `sh -s` over ssh, so a cloud
// fragment would be executed as a shell script. Refusing up front, rather
// than discovering that the hard way, is the whole reason this error is
// typed instead of left to whatever sh made of the YAML.
var ErrAppliedAtBoot = errors.New("recipes for this backend already ran at first boot")

// ApplyOpts controls one Apply call.
type ApplyOpts struct {
	// Only restricts the run to a subset of the VM's OWN recipe list,
	// naming entries exactly as they appear in VM.Recipes (full filenames,
	// e.g. "xfce.alpine.sh", the same identifiers Recipes and CheckRecipes
	// use). Empty means "every recipe the VM has".
	//
	// Only does NOT let a caller run a recipe the VM was not created with:
	// that would bypass the applicability check Create already made
	// (core.go's checkRecipes) for a name nobody vetted against this VM's
	// OS/backend. A caller that wants a recipe added first goes through
	// Update(name, Patch{Recipes: ...}), which is the one place that
	// decision belongs.
	Only []string
}

// Apply runs VM name's recipes over ssh and blocks until they finish.
//
// It is deliberately NEVER implicit (docs/design/core-api.md §6): the only
// way recipes run is this call, made by a caller that already decided to
// make it. Nothing elsewhere in this package invokes it as a side effect of
// Create or Start.
//
// The actual run is sshx.Provision, unchanged, not reimplemented. Apply's
// job is entirely the caller-facing decisions Provision itself has no way to
// make: which VM, which backend, and which subset. Progress is NOT a new
// channel: Provision writes "=== recipe NAME ===" markers to
// v.ProvisionLogPath() exactly as it does when the TUI's autoprov path
// drives it, and internal/tui/provstep.go already knows how to tail that
// file. A caller here gets the identical log through Logs(name, WhichApply)
// or Wait(ctx, name, UntilApplied), both already exist and both already
// read that same file, so nothing here invents a second progress path.
//
// ctx: Apply is the second operation in this package (after Exec) with a
// real reason for one: a desktop recipe can take minutes. sshx.Provision now
// takes ctx itself and runs each recipe under exec.CommandContext, so
// cancelling ctx here kills the in-flight ssh process (SIGTERM, then a grace
// period; see sshx.recipeShutdownGrace) rather than merely stopping this
// call from waiting on it. Provision also checks ctx.Err() between recipes,
// so a cancellation landing in the gap between two doesn't start one more
// ssh process only to kill it immediately. Apply passes ctx straight through
// rather than adding a second layer of cancellation on top: an earlier
// version raced its own goroutine against ctx.Done() because Provision could
// not be cancelled at all, which is no longer true and would now just be a
// redundant, less complete copy of what Provision itself does.
func Apply(ctx context.Context, name string, opts ApplyOpts) error {
	v, err := load(name)
	if err != nil {
		return err
	}
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, name)
	}
	if backend.For(v).Name() == "cloudinit" {
		return fmt.Errorf("%w: %s", ErrAppliedAtBoot, name)
	}

	targets := v.Recipes
	if len(opts.Only) > 0 {
		have := make(map[string]bool, len(v.Recipes))
		for _, r := range v.Recipes {
			have[r] = true
		}
		for _, o := range opts.Only {
			if !have[o] {
				return fmt.Errorf("%w: recipe %q is not one of %s's recipes", ErrRecipeNotApplicable, o, name)
			}
		}
		targets = opts.Only
	}
	if len(targets) == 0 {
		// Nothing to run is not an error: an explicit Apply on a VM with no
		// recipes configured (or Only naming none) is a no-op, matching
		// Create's own "no recipes at all stays valid" rule rather than
		// punishing a caller for asking anyway.
		return nil
	}

	// v2 recipes (docs/recipe-spec-v2.md) declare a run mode in their own
	// recipe.toml; a v1 flat-file recipe has no manifest at all and keeps
	// the old "always run it" behaviour untouched. explicit is which names
	// the caller asked for BY NAME (a non-empty Only): that is what tells a
	// "manual"-run recipe apart from one merely inherited via v.Recipes, the
	// distinction docs/recipe-spec-v2.md's Decisions §4 draws between
	// "stoat apply" and "stoat apply --recipe <name>".
	explicit := make(map[string]bool, len(opts.Only))
	for _, o := range opts.Only {
		explicit[o] = true
	}
	runTargets, manifests, err := filterByRunMode(v, targets, explicit)
	if err != nil {
		return err
	}
	if len(runTargets) == 0 {
		// Every target was skipped by its own run mode (an "once" recipe
		// already applied at its current version, or a "manual" recipe
		// nobody named explicitly). That is not a failure: it is the run
		// mode doing exactly what it declared, so this stays a no-op like
		// the "nothing to run at all" case above rather than erroring.
		return nil
	}

	// run is v with Recipes narrowed to runTargets. config.VM is a plain
	// value struct (see internal/config/config.go), no mutex, no owned
	// resource, so copying it and pointing sshx.Provision at the copy is
	// exactly as safe as calling it on v directly, and it is what lets Only
	// (and now run-mode filtering) reuse Provision as-is instead of it
	// needing a subset parameter added to a file this change may not touch.
	// Dir (and so ProvisionLogPath) is unchanged, which is what keeps this
	// on the SAME log every other reader of this VM already tails.
	run := *v
	run.Recipes = runTargets

	if err := sshx.Provision(ctx, &run); err != nil {
		return err
	}

	// Provision runs runTargets in order and stops at the first failure, so
	// reaching here means every one of them succeeded; each v2 recipe among
	// them (the ones ManifestFor actually found a manifest for) gets its
	// applied state recorded. A v1 recipe has no version to record and is
	// left out of Applied exactly as it always has been.
	var changed bool
	for _, name := range runTargets {
		m, ok := manifests[name]
		if !ok {
			continue
		}
		if v.Applied == nil {
			v.Applied = make(map[string]config.AppliedRecipe, len(runTargets))
		}
		v.Applied[name] = config.AppliedRecipe{Version: m.Version, At: time.Now()}
		changed = true
	}
	if !changed {
		return nil
	}
	return v.Save()
}

// filterByRunMode narrows targets to the recipes that should actually run,
// given each recipe's declared run mode (recipes.Manifest.Run) and what v
// has already recorded in Applied:
//
//   - "manual" never runs implicitly; it only runs when explicit[name] is
//     true, i.e. the caller named it directly via ApplyOpts.Only.
//   - "once" is skipped when v.Applied already has an entry for name AT THE
//     SAME VERSION the manifest declares now; a version bump makes it run
//     again, which is the whole point of recording Version alongside At.
//   - "always" is never skipped.
//
// A target with no recipe.toml at all (ManifestFor's ok=false) is a v1
// flat-file recipe: it has no run-mode concept, so it always stays in the
// result, matching Apply's behaviour before v2 recipes existed. The returned
// map holds the manifest for every v2 recipe kept, so the caller does not
// have to re-resolve it after the run succeeds.
func filterByRunMode(v *config.VM, targets []string, explicit map[string]bool) ([]string, map[string]recipes.Manifest, error) {
	kept := make([]string, 0, len(targets))
	manifests := make(map[string]recipes.Manifest, len(targets))
	for _, name := range targets {
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			kept = append(kept, name)
			continue
		}
		manifests[name] = m

		switch m.Run {
		case "manual":
			if !explicit[name] {
				continue
			}
		case "once":
			if applied, done := v.Applied[name]; done && applied.Version == m.Version {
				continue
			}
		}
		kept = append(kept, name)
	}
	return kept, manifests, nil
}

// Recipe is one recipe resolved for a specific OS. In v2, recipes are
// directories with recipe.toml manifests; Name is the recipe name (e.g.,
// "xfce"), not a filename.
type Recipe struct {
	Name        string // recipe name, matches the directory name
	Description string // from recipe.toml
}

// RecipeFilter selects the recipes Recipes returns: the set
// recipes.List(OS, Backend) would offer a VM with that OS and backend.
// Backend is accepted for API compatibility but ignored in v2 (all recipes
// are shell scripts, the backend determines how they run, not which apply).
type RecipeFilter struct {
	OS      string
	Backend string
}

// Recipes returns what applies to a VM with the given OS.
func Recipes(f RecipeFilter) ([]Recipe, error) {
	if strings.TrimSpace(f.OS) == "" {
		return nil, fmt.Errorf("%w: RecipeFilter needs OS", ErrInvalidSpec)
	}
	manifests, err := recipes.ListManifests()
	if err != nil {
		return nil, err
	}
	var out []Recipe
	for _, m := range manifests {
		if recipes.MatchesVM(&m, f.OS) {
			out = append(out, Recipe{Name: m.Name, Description: m.Description})
		}
	}
	return out, nil
}

// RecipeIssue names one recipe a Spec asked for that osName/backendName
// cannot run, and why, never a bare bool. See CheckRecipes.
type RecipeIssue struct {
	Name   string
	Reason string
}

// CheckRecipes validates names against osName/backendName the same way
// Create's own checkRecipes does internally (core.go), but as a standalone
// call a caller can make BEFORE building a Spec at all, and it explains each
// failure instead of only naming what else is available.
//
// Only entries that are NOT applicable come back: a name that resolves
// fine has nothing to report, so a caller checking "will these work" reads
// an empty result as a clean answer rather than having to filter out an "OK"
// entry per name.
//
// The reasons below prefer a recipe's own declared front matter
// (internal/recipes.ParseMetadata/UnsupportedReason) when it has any: "xfce
// requires systemd, alpine uses openrc", the exact example in
// docs/design/core-api.md §4, is real now. Where a recipe declares no
// metadata (every *.cloud.yaml fragment today, by design; see
// recipeIssueReason) the reason falls back to one derived structurally:
// from the requested file's own name, from guest.Lookup(osName), and from
// what else exists on disk (an OS-specific override suppressing a shared
// fragment; see recipes.List's own doc comment on "overridden"). So
// "xfce.cloud.yaml is not offered to alpine because alpine has its own
// xfce.alpine.cloud.yaml" is still a real, true structural reason this can
// give, for exactly the recipes that have no better one to give instead.
func CheckRecipes(osName, backendName string, names []string) ([]RecipeIssue, error) {
	available, err := recipes.List(osName, backendName)
	if err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(available))
	for _, a := range available {
		ok[a] = true
	}

	var issues []RecipeIssue
	for _, n := range names {
		if ok[n] {
			continue
		}
		issues = append(issues, RecipeIssue{Name: n, Reason: recipeIssueReason(n, osName, backendName)})
	}
	return issues, nil
}

// recipeIssueReason explains why name is not offered to osName/backendName.
// It prefers a capability-based reason drawn from the recipe's own declared
// front matter (internal/recipes.ParseMetadata/UnsupportedReason), and falls
// back to one derived structurally, from the filename, guest.Lookup, and
// whether other files exist on disk, when the recipe declares no metadata;
// see CheckRecipes' doc comment for why a structural fallback still exists
// at all.
func recipeIssueReason(name, osName, backendName string) string {
	if _, err := os.Stat(recipes.Path(name)); err != nil {
		return fmt.Sprintf("no such recipe %q", name)
	}

	isCloudFragment := strings.HasSuffix(name, ".cloud.yaml")
	isShellRecipe := strings.HasSuffix(name, ".sh") && !isCloudFragment

	// The backend mismatches below (a shell recipe pushed to a cloudinit VM,
	// or a cloud fragment offered to a backend with no cloud-init seed to
	// merge it into) are checked before metadata and win outright: they are
	// about HOW a recipe gets applied, which no front-matter tag declares,
	// so there is nothing for UnsupportedReason to say that would improve on
	// this.
	switch {
	case isCloudFragment && backendName != "cloudinit":
		return fmt.Sprintf("%s is a cloud-init fragment; the %s backend applies recipes over ssh after boot, not from a cloud-init seed", name, backendName)
	case isShellRecipe && backendName == "cloudinit":
		return fmt.Sprintf("%s is a shell recipe pushed over ssh; the cloudinit backend applies its recipes from the seed at first boot instead", name)
	}

	// Backend is applicable; whether name is a match for osName is what a
	// recipe's own declared front matter, when present, can now answer with
	// a real reason ("requires systemd, alpine uses openrc") instead of one
	// guessed from the filename, and that reason wins when both exist. A
	// parse error or a recipe with no "# stoat:" block at all (every
	// *.cloud.yaml fragment today; see docs/design/guest-subsystem.md §5's
	// phase split) leaves m at its zero value, UnsupportedReason then
	// returns "", and the structural switch below is what actually answers.
	if m, err := recipes.ReadMetadata(name); err == nil {
		if reason := recipes.UnsupportedReason(osName, m); reason != "" {
			return fmt.Sprintf("%s: %s", name, reason)
		}
	}

	switch {
	case isCloudFragment:
		base := strings.TrimSuffix(name, ".cloud.yaml")
		if i := strings.LastIndex(base, "."); i >= 0 {
			fileOS := base[i+1:]
			return fmt.Sprintf("%s is built for %s, not %s", name, fileOS, osName)
		}
		g, isKnown := guest.Lookup(osName)
		if !isKnown {
			return fmt.Sprintf("%s: %q is not a recognised OS", name, osName)
		}
		if !g.CloudRecipes {
			return fmt.Sprintf("%s: %s is not in the shared cloud-recipe set", name, osName)
		}
		if override := base + "." + osName + ".cloud.yaml"; fileExists(recipes.Path(override)) {
			return fmt.Sprintf("%s: %s has its own override, %s; use that instead of the shared fragment", name, osName, override)
		}
		return fmt.Sprintf("%s is not offered to %s/%s", name, osName, backendName)
	case isShellRecipe:
		fields := strings.Split(strings.TrimSuffix(name, ".sh"), ".")
		fileOS := fields[len(fields)-1]
		return fmt.Sprintf("%s is built for %s, not %s", name, fileOS, osName)
	default:
		return fmt.Sprintf("%s is not offered to %s/%s", name, osName, backendName)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
