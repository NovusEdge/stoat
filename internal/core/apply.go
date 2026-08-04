package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/sshx"
)

// ErrAppliedAtBoot is returned by Apply for a VM whose backend already ran
// its recipes at first boot and has no post-boot apply step at all — the
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
	// e.g. "xfce.alpine.sh" — the same identifiers Recipes and CheckRecipes
	// use). Empty means "every recipe the VM has".
	//
	// Only does NOT let a caller run a recipe the VM was not created with —
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
// The actual run is sshx.Provision — unchanged, not reimplemented. Apply's
// job is entirely the caller-facing decisions Provision itself has no way to
// make: which VM, which backend, and which subset. Progress is NOT a new
// channel: Provision writes "=== recipe NAME ===" markers to
// v.ProvisionLogPath() exactly as it does when the TUI's autoprov path
// drives it, and internal/tui/provstep.go already knows how to tail that
// file. A caller here gets the identical log through Logs(name, WhichApply)
// or Wait(ctx, name, UntilApplied) — both already exist and both already
// read that same file, so nothing here invents a second progress path.
//
// ctx: Apply is the second operation in this package (after Exec) with a
// real reason for one — a desktop recipe can take minutes. sshx.Provision now
// takes ctx itself and runs each recipe under exec.CommandContext, so
// cancelling ctx here kills the in-flight ssh process (SIGTERM, then a grace
// period — see sshx.recipeShutdownGrace) rather than merely stopping this
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

	// run is v with Recipes narrowed to targets. config.VM is a plain value
	// struct (see internal/config/config.go) — no mutex, no owned resource —
	// so copying it and pointing sshx.Provision at the copy is exactly as
	// safe as calling it on v directly, and it is what lets Only reuse
	// Provision as-is instead of it needing a subset parameter added to a
	// file this change may not touch. Dir (and so ProvisionLogPath) is
	// unchanged, which is what keeps this on the SAME log every other
	// reader of this VM already tails.
	run := *v
	run.Recipes = targets

	return sshx.Provision(ctx, &run)
}

// Recipe is one recipe file resolved for a specific (OS, backend) pair, with
// what can be honestly derived about it from its filename and its place on
// disk.
//
// docs/design/guest-subsystem.md §5 specifies an in-band front-matter
// contract — "# stoat:name", "# stoat:description", "# stoat:os",
// "# stoat:requires", "# stoat:stages" comments a recipe script would
// declare — as the source for a recipe's real metadata. THAT PARSER DOES
// NOT EXIST. internal/recipes has no code that reads those tags, and no
// recipe file in the tree carries them: every .sh and .yaml recipe here has
// only free-form prose comments (checked directly — xfce.alpine.sh,
// xfce.debian.cloud.yaml, devtools.cloud.yaml, all of them). So Recipe
// cannot report a declared Description, Requires, or Stages without
// inventing a format nobody has agreed to, which is exactly the gap this
// change was told to report rather than paper over. What IS populated below
// is derived purely from the filename and from what else exists on disk —
// no script content is parsed, no comment is treated as documentation.
type Recipe struct {
	// Name is the full filename ("xfce.alpine.cloud.yaml"), exactly what
	// ApplyOpts.Only, Spec.Recipes and recipes.Read all expect.
	Name string
	// Label is the recipe's short display name: Name with its .sh/.yaml
	// extension and its OS/backend-suffix segment stripped
	// ("xfce.alpine.sh" -> "xfce"). Mirrors internal/tui/labels.go's
	// recipeLabel exactly (that function is unexported in package tui,
	// which this package must not import — tui sits above core in the
	// layering, and reaching back down would be the cycle
	// docs/design/guest-subsystem.md §3.2 explicitly avoids for a related
	// reason). Kept here as a small, independent copy rather than a shared
	// export, since the two calls are one line each.
	Label string
	// TargetOS is the OS this specific file is pinned to, read off its own
	// filename ("alpine" for "xfce.alpine.sh", "debian" for
	// "xfce.debian.cloud.yaml"). Empty for a SHARED cloud-config fragment —
	// see Shared.
	TargetOS string
	// Shared reports whether this is a cloud-config fragment offered to
	// every guest.OS with CloudRecipes set (e.g. "devtools.cloud.yaml"),
	// rather than one pinned to a single OS by its filename. Always false
	// for a shell (.sh) recipe, which is always OS-pinned by convention
	// (see recipes.List's doc comment).
	Shared bool
}

// RecipeFilter selects the recipes Recipes returns: the exact set
// recipes.List(OS, Backend) would offer a VM with that OS and backend,
// each with what Recipe above can honestly report about it. Both fields are
// required — there is no "every OS" or "every backend" scope, matching
// recipes.List itself, which cannot answer that question either.
type RecipeFilter struct {
	OS      string
	Backend string
}

// Recipes returns what applies to a VM with the given OS and backend, with
// the metadata described on Recipe.
func Recipes(f RecipeFilter) ([]Recipe, error) {
	if strings.TrimSpace(f.OS) == "" || strings.TrimSpace(f.Backend) == "" {
		return nil, fmt.Errorf("%w: RecipeFilter needs both OS and Backend", ErrInvalidSpec)
	}
	names, err := recipes.List(f.OS, f.Backend)
	if err != nil {
		return nil, err
	}
	out := make([]Recipe, 0, len(names))
	for _, n := range names {
		out = append(out, recipeMetadata(n, f.Backend))
	}
	return out, nil
}

// recipeLabel mirrors internal/tui/labels.go's function of the same name —
// see Recipe.Label's comment for why this is a small independent copy
// rather than a shared export.
func recipeLabel(file string) string {
	name := strings.TrimSuffix(file, ".yaml")
	name = strings.TrimSuffix(name, ".sh")
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// recipeMetadata builds a Recipe for name, a filename recipes.List already
// confirmed applies to backendName. The OS-suffix parsing here matches
// recipes.List's own (internal/recipes/recipes.go) exactly, on purpose —
// diverging would make Recipe's TargetOS disagree with why the file was
// even in the list.
func recipeMetadata(name, backendName string) Recipe {
	r := Recipe{Name: name, Label: recipeLabel(name)}
	if backendName == "cloudinit" && strings.HasSuffix(name, ".cloud.yaml") {
		base := strings.TrimSuffix(name, ".cloud.yaml")
		if i := strings.LastIndex(base, "."); i >= 0 {
			r.TargetOS = base[i+1:]
		} else {
			r.Shared = true
		}
		return r
	}
	if strings.HasSuffix(name, ".sh") {
		fields := strings.Split(strings.TrimSuffix(name, ".sh"), ".")
		r.TargetOS = fields[len(fields)-1]
	}
	return r
}

// RecipeIssue names one recipe a Spec asked for that osName/backendName
// cannot run, and why — never a bare bool. See CheckRecipes.
type RecipeIssue struct {
	Name   string
	Reason string
}

// CheckRecipes validates names against osName/backendName the same way
// Create's own checkRecipes does internally (core.go), but as a standalone
// call a caller can make BEFORE building a Spec at all, and it explains each
// failure instead of only naming what else is available.
//
// Only entries that are NOT applicable come back — a name that resolves
// fine has nothing to report, so a caller checking "will these work" reads
// an empty result as a clean answer rather than having to filter out an "OK"
// entry per name.
//
// The reasons below are derived structurally: from the requested file's own
// name, from guest.Lookup(osName), and from what else exists on disk (an
// OS-specific override suppressing a shared fragment — see recipes.List's
// own doc comment on "overridden"). They do NOT draw on a recipe's declared
// requirements ("requires: systemd" and the like) — as Recipe's doc comment
// explains, that front-matter contract is not implemented anywhere in
// internal/recipes yet. So "xfce.cloud.yaml is not offered to alpine
// because alpine has its own xfce.alpine.cloud.yaml" is a real, true reason
// this can give today; "xfce requires systemd, alpine uses openrc" — the
// exact example in docs/design/core-api.md §4 — is not, until that parser
// exists.
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

// recipeIssueReason explains why name is not offered to osName/backendName,
// without consulting anything beyond the filename, guest.Lookup, and
// whether other files exist on disk — see CheckRecipes' doc comment on why
// it stops there.
func recipeIssueReason(name, osName, backendName string) string {
	if _, err := os.Stat(recipes.Path(name)); err != nil {
		return fmt.Sprintf("no such recipe %q", name)
	}

	isCloudFragment := strings.HasSuffix(name, ".cloud.yaml")
	isShellRecipe := strings.HasSuffix(name, ".sh") && !isCloudFragment

	switch {
	case isCloudFragment && backendName != "cloudinit":
		return fmt.Sprintf("%s is a cloud-init fragment; the %s backend applies recipes over ssh after boot, not from a cloud-init seed", name, backendName)
	case isShellRecipe && backendName == "cloudinit":
		return fmt.Sprintf("%s is a shell recipe pushed over ssh; the cloudinit backend applies its recipes from the seed at first boot instead", name)
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
