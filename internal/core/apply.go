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
// its recipes at first boot: the cloudinit backend. Its recipes merge into
// the cloud-init seed and run through the guest's own cloud-init service,
// before sshd is even reachable (internal/backend/cloudinit.go's Prepare has
// no matching Provision). Calling sshx.Provision on such a VM is not a
// harmless no-op: v.Recipes holds "*.cloud.yaml" fragment names, and
// Provision pipes whatever it's given to `sh -s` over ssh, so a cloud
// fragment would run as a shell script. Apply refuses up front instead.
var ErrAppliedAtBoot = errors.New("recipes for this backend already ran at first boot")

// ApplyOpts controls one Apply call.
type ApplyOpts struct {
	// Only restricts the run to a subset of the VM's OWN recipe list,
	// naming entries exactly as they appear in VM.Recipes (full filenames,
	// e.g. "xfce.alpine.sh", the identifiers Recipes and CheckRecipes use).
	// Empty means every recipe the VM has.
	//
	// Only cannot name a recipe the VM was not created with. That would
	// bypass the applicability check Create already made (core.go's
	// checkRecipes) for a name nobody vetted against this VM's OS/backend.
	// A caller that wants a recipe added first calls
	// Update(name, Patch{Recipes: ...}).
	Only []string
}

// Apply runs VM name's recipes over ssh and blocks until they finish.
//
// Apply never runs implicitly (docs/design/core-api.md §6). Recipes run
// only through this call. Nothing else in this package invokes it as a side
// effect of Create or Start.
//
// The run itself is sshx.Provision, unchanged. Apply only makes the
// caller-facing decisions Provision cannot: which VM, which backend, which
// subset. Progress uses no new channel: Provision writes "=== recipe NAME
// ===" markers to v.ProvisionLogPath(), the same file the TUI's autoprov
// path and internal/tui/provstep.go already tail. A caller here reads the
// same log through Logs(name, WhichApply) or Wait(ctx, name, UntilApplied).
//
// ctx matters here because a desktop recipe can take minutes. sshx.Provision
// takes ctx and runs each recipe under exec.CommandContext, so cancelling
// ctx kills the in-flight ssh process (SIGTERM, then a grace period; see
// sshx.recipeShutdownGrace) instead of only stopping this call from
// waiting. Provision also checks ctx.Err() between recipes, so a
// cancellation in the gap between two recipes does not start one more ssh
// process just to kill it. Apply passes ctx straight through and adds no
// second cancellation layer of its own.
//
// Apply holds an exclusive flock on v.Dir/provision.lock for the whole run.
// Two concurrent runs against the same VM would each start apk (or the
// guest's own package manager) and race on its database lock; the second
// Apply sees the lock held and returns ErrProvisionInProgress instead.
func Apply(ctx context.Context, name string, opts ApplyOpts) error {
	v, err := load(name)
	if err != nil {
		return err
	}
	err = WithProvisionLock(v.Dir, func() error {
		return applyLocked(ctx, v, opts)
	})
	if errors.Is(err, ErrProvisionInProgress) {
		return fmt.Errorf("%w: %s", ErrProvisionInProgress, name)
	}
	return err
}

// applyLocked is Apply's body, run while Apply holds name's provision lock.
func applyLocked(ctx context.Context, v *config.VM, opts ApplyOpts) error {
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, v.Name)
	}
	if backend.For(v).Name() == "cloudinit" {
		return fmt.Errorf("%w: %s", ErrAppliedAtBoot, v.Name)
	}

	targets := v.Recipes
	if len(opts.Only) > 0 {
		have := make(map[string]bool, len(v.Recipes))
		for _, r := range v.Recipes {
			have[r] = true
		}
		for _, o := range opts.Only {
			if !have[o] {
				return fmt.Errorf("%w: recipe %q is not one of %s's recipes", ErrRecipeNotApplicable, o, v.Name)
			}
		}
		targets = opts.Only
	}
	if len(targets) == 0 {
		// Nothing to run is not an error. An explicit Apply on a VM with no
		// recipes configured, or Only naming none, is a no-op, matching
		// Create's own "no recipes at all stays valid" rule.
		return nil
	}

	// v2 recipes (docs/recipe-spec-v2.md) declare a run mode in their own
	// recipe.toml. A v1 flat-file recipe has no manifest and keeps the old
	// "always run it" behavior. explicit holds the names the caller asked
	// for BY NAME, a non-empty Only. It tells a "manual"-run recipe apart
	// from one merely inherited via v.Recipes, the distinction
	// docs/recipe-spec-v2.md's Decisions §4 draws between "stoat apply" and
	// "stoat apply --recipe <name>".
	explicit := make(map[string]bool, len(opts.Only))
	for _, o := range opts.Only {
		explicit[o] = true
	}
	runTargets, manifests, err := filterByRunMode(v, targets, explicit)
	if err != nil {
		return err
	}
	if len(runTargets) == 0 {
		// Every target was skipped by its own run mode: an "once" recipe
		// already applied at its current version, or a "manual" recipe
		// nobody named explicitly. This is not a failure. It is the run
		// mode doing what it declared, so this stays a no-op like the
		// "nothing to run at all" case above.
		return nil
	}

	// run is v with Recipes narrowed to runTargets. config.VM is a plain
	// value struct (internal/config/config.go) with no mutex and no owned
	// resource, so copying it and pointing sshx.Provision at the copy is as
	// safe as calling it on v directly. This lets Only and run-mode
	// filtering reuse Provision as-is, with no subset parameter added to
	// it. Dir (and so ProvisionLogPath) is unchanged, so this stays on the
	// same log every other reader of this VM already tails.
	run := *v
	run.Recipes = runTargets

	if err := sshx.Provision(ctx, &run); err != nil {
		return err
	}

	// Provision runs runTargets in order and stops at the first failure, so
	// reaching here means every one succeeded. Each v2 recipe among them
	// (one ManifestFor found a manifest for) gets its applied state
	// recorded. A v1 recipe has no version to record and stays out of
	// Applied, as it always has.
	var changed bool
	for _, name := range runTargets {
		m, ok := manifests[name]
		if !ok {
			continue
		}
		if v.Applied == nil {
			v.Applied = make(map[string]config.AppliedRecipe, len(runTargets))
		}
		hash, err := recipes.ScriptHash(name, v.OS)
		if err != nil {
			return err
		}
		v.Applied[name] = config.AppliedRecipe{Version: m.Version, Hash: hash, At: time.Now()}
		changed = true
	}
	if !changed {
		return nil
	}
	return v.Save()
}

// filterByRunMode narrows targets to the recipes that should actually run,
// given each recipe's declared run mode (recipes.Manifest.Run) and what v
// has already recorded in Applied.
//
// "manual" never runs implicitly. It runs only when explicit[name] is true,
// meaning the caller named it directly via ApplyOpts.Only. "once" is
// skipped when v.Applied already has an entry for name whose Hash matches
// the script that would run now; a changed script reruns even at the same
// manifest version, since a version bump is not the only way a recipe
// author fixes one. "always" is never skipped.
//
// An Applied entry saved before Hash existed decodes with an empty string.
// That never equals a real script hash, so an existing VM reruns its
// "once" recipes exactly once, then carries a real hash from then on.
//
// A target with no recipe.toml (ManifestFor's ok=false) is a v1 flat-file
// recipe. It has no run-mode concept and always stays in the result,
// matching Apply's behavior before v2 recipes existed. The returned map
// holds the manifest for every v2 recipe kept, so the caller does not have
// to re-resolve it after the run succeeds.
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
			if applied, done := v.Applied[name]; done {
				hash, err := recipes.ScriptHash(name, v.OS)
				if err != nil {
					return nil, nil, err
				}
				if applied.Hash == hash {
					continue
				}
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
// call a caller can make before building a Spec, and it explains each
// failure instead of only naming what else is available.
//
// Only entries that are NOT applicable come back. A name that resolves fine
// reports nothing, so a caller checking "will these work" reads an empty
// result as a clean answer.
//
// The reasons below prefer a recipe's own declared front matter
// (internal/recipes.ParseMetadata/UnsupportedReason) when it has any: "xfce
// requires systemd, alpine uses openrc" is docs/design/core-api.md §4's
// example. Where a recipe declares no metadata (every *.cloud.yaml fragment
// today, by design; see recipeIssueReason) the reason falls back to a
// structural one, derived from the requested file's name, from
// guest.Lookup(osName), and from what else exists on disk (an OS-specific
// override suppressing a shared fragment; see recipes.List's doc comment on
// "overridden"). That still gives a true reason like "xfce.cloud.yaml is
// not offered to alpine because alpine has its own xfce.alpine.cloud.yaml"
// for recipes with no better reason to give.
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
// front matter (internal/recipes.ParseMetadata/UnsupportedReason). When the
// recipe declares no metadata, it falls back to one derived structurally,
// from the filename, guest.Lookup, and whether other files exist on disk;
// see CheckRecipes' doc comment for why that fallback exists.
func recipeIssueReason(name, osName, backendName string) string {
	if _, err := os.Stat(recipes.Path(name)); err != nil {
		return fmt.Sprintf("no such recipe %q", name)
	}

	isCloudFragment := strings.HasSuffix(name, ".cloud.yaml")
	isShellRecipe := strings.HasSuffix(name, ".sh") && !isCloudFragment

	// The backend mismatches below are checked before metadata and win
	// outright: a shell recipe pushed to a cloudinit VM, or a cloud
	// fragment offered to a backend with no cloud-init seed to merge it
	// into. Both are about HOW a recipe gets applied, which no front-matter
	// tag declares, so UnsupportedReason has nothing better to say.
	switch {
	case isCloudFragment && backendName != "cloudinit":
		return fmt.Sprintf("%s is a cloud-init fragment; the %s backend applies recipes over ssh after boot, not from a cloud-init seed", name, backendName)
	case isShellRecipe && backendName == "cloudinit":
		return fmt.Sprintf("%s is a shell recipe pushed over ssh; the cloudinit backend applies its recipes from the seed at first boot instead", name)
	}

	// Backend is applicable. Whether name matches osName is answered by the
	// recipe's own declared front matter, when present, with a real reason
	// ("requires systemd, alpine uses openrc") instead of one guessed from
	// the filename; that reason wins when both exist. A parse error or a
	// recipe with no "# stoat:" block (every *.cloud.yaml fragment today;
	// see docs/design/guest-subsystem.md §5's phase split) leaves m at its
	// zero value. UnsupportedReason then returns "", and the structural
	// switch below answers instead.
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
