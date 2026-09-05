package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/sshx"
)

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

// ApplyPlan is one recipe's entry in a dry-run: what Apply would do with it,
// and why. Version is the recipe's applied version from v.Applied, empty when
// it was never applied.
type ApplyPlan struct {
	Name    string `json:"name"`
	Action  string `json:"action"` // "run" | "skip"
	Reason  string `json:"reason"`
	Version string `json:"version,omitempty"`
}

// PlanApply reports what Apply would do for VM name, without running anything.
// It uses the same filtering as applyLocked (run mode, dependency order and
// satisfaction), so a plan matches the run that would follow.
//
// The plan is host-side: it reads manifests and v.Applied, so the VM does not
// need to be running. A cloudinit VM whose markers were never discovered still
// has an empty v.Applied here, so its recipes read as "never applied" until a
// real Apply populates it (docs/specs recipe-system-fixes §3 fallback).
func PlanApply(name string, opts ApplyOpts) ([]ApplyPlan, error) {
	v, err := load(name)
	if err != nil {
		return nil, err
	}

	targets, err := resolveTargets(v, opts.Only)
	if err != nil {
		return nil, err
	}
	explicit := make(map[string]bool, len(opts.Only))
	for _, o := range opts.Only {
		explicit[o] = true
	}

	decisions, _, err := planRecipes(v, targets, explicit)
	if err != nil {
		return nil, err
	}
	plan := make([]ApplyPlan, 0, len(decisions))
	for _, d := range decisions {
		action := "skip"
		if d.run {
			action = "run"
		}
		plan = append(plan, ApplyPlan{Name: d.name, Action: action, Reason: d.reason, Version: v.Applied[d.name].Version})
	}
	return plan, nil
}

// resolveTargets validates only against v's own recipes and returns the
// target list: only itself when non-empty, v.Recipes otherwise. Shared by
// PlanApply and applyLocked so a dry-run plan always targets the same
// recipes a real Apply would.
func resolveTargets(v *config.VM, only []string) ([]string, error) {
	if len(only) == 0 {
		return v.Recipes, nil
	}
	have := make(map[string]bool, len(v.Recipes))
	for _, r := range v.Recipes {
		have[r] = true
	}
	for _, o := range only {
		if !have[o] {
			return nil, fmt.Errorf("%w: recipe %q is not one of %s's recipes", ErrRecipeNotApplicable, o, v.Name)
		}
	}
	return only, nil
}

// applyLocked is Apply's body, run while Apply holds name's provision lock.
func applyLocked(ctx context.Context, v *config.VM, opts ApplyOpts) error {
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, v.Name)
	}

	targets, err := resolveTargets(v, opts.Only)
	if err != nil {
		return err
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

	// A cloudinit VM ran its recipes from the seed at first boot and never
	// populated v.Applied. Read the marker files cloud-init left behind, so
	// filterByRunMode can skip a "once" recipe instead of re-running it.
	if err := discoverCloudInitApplied(ctx, v); err != nil {
		return err
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
	if v.Applied == nil {
		v.Applied = make(map[string]config.AppliedRecipe, len(runTargets))
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
	rebootRecipe, needsReboot := "", false
	for _, name := range runTargets {
		m, ok := manifests[name]
		if !ok {
			continue
		}
		if v.Applied == nil {
			v.Applied = make(map[string]config.AppliedRecipe, len(runTargets))
		}
		hash, err := recipeHashFor(v, m)
		if err != nil {
			return err
		}
		scriptHash, err := recipes.ScriptHash(name, v.OS)
		if err != nil {
			return err
		}
		provisioned := run.Applied[name]
		v.Applied[name] = config.AppliedRecipe{
			Version: m.Version, Hash: hash, ScriptHash: scriptHash, At: time.Now(),
			Outputs: provisioned.Outputs, Health: string(HealthUnknown),
		}
		if m.Reboot && !needsReboot {
			rebootRecipe, needsReboot = name, true
		}
	}

	// One recipe declaring reboot=true is enough: the guest reboots once,
	// not once per such recipe, so the first one found names the reboot in
	// the log.
	//
	// Disk only: a live VM's root is a tmpfs the reboot wipes, and a live VM
	// re-applies every boot, so a reboot here would loop. A live desktop
	// recipe restarts its session in place instead (xfce's kill -HUP 1).
	if needsReboot && v.Mode == "disk" {
		if err := rebootAndWait(ctx, v, rebootRecipe); err != nil {
			return err
		}
	}

	verdicts, healthErr := healthChecksForVM(ctx, v, runTargets)
	if healthErr != nil {
		if saveErr := v.Save(); saveErr != nil {
			return saveErr
		}
		return healthErr
	}
	if err := v.Save(); err != nil {
		return err
	}
	for _, verdict := range verdicts {
		if verdict.Status == HealthFailed {
			return fmt.Errorf("%s: %s", verdict.Name, verdict.Detail)
		}
	}
	return nil
}

// rebootAndWait reboots v's guest over ssh and waits for it to come back.
//
// Some recipes change something the running kernel or init system only
// picks up at boot (xfce's setup-devd switches Alpine's device manager from
// mdev to udev, which Xorg needs for its mouse and keyboard to work). Their
// manifest declares reboot=true so Apply reboots the guest once, here,
// after every recipe in the run has already succeeded.
func rebootAndWait(ctx context.Context, v *config.VM, recipe string) error {
	appendProvisionLog(v, fmt.Sprintf("rebooting %s to finish %s...\n", v.Name, recipe))

	// `reboot` tears down the ssh session before the process can report an
	// exit status back to this host, so cmd.Run() returning an error here is
	// expected and not a failure signal; only the wait below is.
	cmd := exec.CommandContext(ctx, "ssh", sshx.Args(v, "reboot")...)
	_ = cmd.Run()

	// The pre-reboot sshd can keep answering for a moment after the reboot
	// command returns. This settle avoids waitReachable's first check
	// catching that dying instance and returning before the guest has
	// actually gone down.
	select {
	case <-time.After(rebootSettle):
	case <-ctx.Done():
		return ctx.Err()
	}

	return waitReachable(ctx, v)
}

// rebootSettle is a guess, not a measurement: real hardware or CI timing
// could need longer for sshd to actually stop answering after `reboot`
// returns control to the caller.
const rebootSettle = 2 * time.Second

// appendProvisionLog appends s to v's apply log, the same file
// sshx.Provision just wrote to and closed. A failure to open it is not
// fatal to the reboot itself, so this drops the error rather than aborting
// an otherwise successful apply over a log write.
func appendProvisionLog(v *config.VM, s string) {
	f, err := os.OpenFile(v.ProvisionLogPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(s)
}

// discoverCloudInitApplied rebuilds v.Applied for a cloudinit VM from the
// marker files cloud-init left after first boot. It runs over ssh, so the VM
// must be reachable; applyLocked calls it only after the qemu.Running check.
//
// It no-ops unless the backend is cloudinit and v.Applied is still empty: once
// a post-boot Apply has recorded state, that state is authoritative and this
// must not overwrite it. A missing marker directory (an old VM, or cloud-init
// that failed entirely) leaves v.Applied empty, so the next Apply re-runs
// every recipe once, then tracks state from then on.
//
// The recorded Hash comes from the current script on disk, not from whatever
// cloud-init ran at creation. That is benign: recipes are idempotent, and a
// script that has since changed reruns on this same Apply anyway.
func discoverCloudInitApplied(ctx context.Context, v *config.VM) error {
	if backend.For(v).Name() != "cloudinit" || len(v.Applied) > 0 {
		return nil
	}
	script := fmt.Sprintf("for marker in %s/*; do case \"$marker\" in *.out) continue;; esac; [ -f \"$marker\" ] || continue; name=$(basename \"$marker\"); printf '===%%s\\n' \"$name\"; cat \"$marker.out\" 2>/dev/null; done", cloudinit.MarkerDir)
	out, err := exec.CommandContext(ctx, "ssh", sshx.Args(v, script)...).Output()
	if err != nil {
		return nil // marker dir missing or a transient ssh error; discover nothing
	}

	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		return err
	}
	var applied map[string]config.AppliedRecipe
	for name, body := range cloudInitOutputs(string(out)) {
		m, ok, manifestErr := recipes.ManifestFor(name)
		if manifestErr != nil || !ok {
			continue // a marker for a recipe no longer on disk
		}
		hash, hashErr := recipeHashFor(v, m)
		if hashErr != nil {
			hash, _ = recipes.ScriptHash(name, v.OS)
		}
		scriptHash, _ := recipes.ScriptHash(name, v.OS)
		values, _ := sshx.ParseOutputs(m.Outputs, redactCloudSecrets(body, secrets[name]))
		if applied == nil {
			applied = make(map[string]config.AppliedRecipe)
		}
		applied[name] = config.AppliedRecipe{
			Version: m.Version, Hash: hash, ScriptHash: scriptHash,
			At: time.Now(), Outputs: values, Health: string(HealthUnknown),
		}
	}
	if applied == nil {
		return nil
	}
	v.Applied = applied
	return v.Save()
}

func cloudInitOutputs(body string) map[string]string {
	out := map[string]string{}
	var name string
	var value strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "===") {
			if name != "" {
				out[name] = value.String()
			}
			name = strings.TrimSpace(strings.TrimPrefix(line, "==="))
			value.Reset()
			continue
		}
		if name != "" {
			value.WriteString(line)
			value.WriteByte('\n')
		}
	}
	if name != "" {
		out[name] = value.String()
	}
	return out
}

func redactCloudSecrets(value string, secrets map[string]string) string {
	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			names = append(names, secret)
		}
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, secret := range names {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
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
// A target with no recipe.toml (ManifestFor's ok=false) is a recipe that
// went missing since the VM was created. It has no script to run, so this
// errors rather than passing a name Provision cannot resolve. The returned
// map holds the manifest for every recipe kept, so the caller does not have
// to re-resolve it after the run succeeds.
//
// Targets run in dependency order, not v.Recipes order: a recipe with
// depends = ["docker"] runs after docker regardless of array position. A
// dependency cycle among the targets errors here, since a manifest edited on
// disk after add-time can introduce one (docs/specs recipe-system-fixes §2).
// A kept recipe's dependency must be satisfied: it ran earlier in this run,
// or is already recorded in v.Applied. An unsatisfiable dependency errors
// rather than run the dependent against an unmet one.
func filterByRunMode(v *config.VM, targets []string, explicit map[string]bool) ([]string, map[string]recipes.Manifest, error) {
	decisions, manifests, err := planRecipes(v, targets, explicit)
	if err != nil {
		return nil, nil, err
	}
	kept := make([]string, 0, len(decisions))
	for _, d := range decisions {
		if d.run {
			kept = append(kept, d.name)
		}
	}
	return kept, manifests, nil
}

// recipeDecision is planRecipes' verdict for one target: whether it runs, and
// the reason a reader (dry-run) can print. run==false is a skip.
type recipeDecision struct {
	name   string
	run    bool
	reason string
}

// planRecipes resolves, dependency-orders, and classifies targets. It returns
// one decision per target in run order. It is the shared body behind both
// filterByRunMode (which keeps the run==true names) and PlanApply (which
// reports every decision). It raises the same errors either way: a missing
// recipe.toml, a dependency cycle, or an unsatisfiable dependency.
func planRecipes(v *config.VM, targets []string, explicit map[string]bool) ([]recipeDecision, map[string]recipes.Manifest, error) {
	manifests := make(map[string]recipes.Manifest, len(targets))
	for _, name := range targets {
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("%w: recipe %q has no recipe.toml", ErrRecipeNotApplicable, name)
		}
		manifests[name] = m
	}

	ordered, err := recipes.TopoSort(targets, manifests)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrRecipeNotApplicable, err)
	}

	decisions := make([]recipeDecision, 0, len(ordered))
	keptSet := make(map[string]bool, len(ordered))
	for _, name := range ordered {
		m := manifests[name]

		run, reason := true, "runs every time"
		switch m.Run {
		case "manual":
			if explicit[name] {
				reason = "named explicitly"
			} else {
				run, reason = false, "manual, not selected"
			}
		case "once":
			if applied, done := v.Applied[name]; done {
				hash, err := recipeHashFor(v, m)
				if err != nil {
					return nil, nil, err
				}
				switch {
				case applied.Hash == hash:
					run, reason = false, "already applied"
				case scriptUnchanged(v, m, applied):
					reason = "params changed"
				default:
					reason = "script changed"
				}
			} else {
				reason = "never applied"
			}
		}

		if run {
			for _, dep := range m.Depends {
				if keptSet[dep] {
					continue // ran earlier in this run
				}
				if _, done := v.Applied[dep]; done {
					continue // already applied on this VM
				}
				return nil, nil, dependencyError(name, dep, manifests, explicit)
			}
			keptSet[name] = true
		}
		decisions = append(decisions, recipeDecision{name: name, run: run, reason: reason})
	}
	return decisions, manifests, nil
}

// recipeHashFor computes the current combined hash for a VM's recipe. Only
// declared secret parameters with non-empty stored values affect the hash;
// stale keys in secrets.toml do not.
func recipeHashFor(v *config.VM, m recipes.Manifest) (string, error) {
	if len(m.Params) == 0 {
		return recipes.ScriptHash(m.Name, v.OS)
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		return "", err
	}
	params, err := recipes.Resolve(m, v.Params[m.Name], secrets[m.Name])
	if err != nil {
		return "", err
	}
	nonSecret := make(map[string]string, len(params))
	for name, value := range params {
		if m.Params[name].Type != "secret" {
			nonSecret[name] = value
		}
	}
	setSecrets := make(map[string]bool)
	for _, name := range m.SecretNames() {
		if secrets[m.Name][name] != "" {
			setSecrets[name] = true
		}
	}
	secretNames := make([]string, 0, len(setSecrets))
	for name := range setSecrets {
		secretNames = append(secretNames, name)
	}
	return recipes.RecipeHash(m.Name, v.OS, nonSecret, secretNames)
}

// scriptUnchanged distinguishes a parameter change from a changed recipe
// body. Entries written before ScriptHash existed cannot make that claim.
func scriptUnchanged(v *config.VM, m recipes.Manifest, applied config.AppliedRecipe) bool {
	if applied.ScriptHash == "" {
		return false
	}
	body, err := recipes.ScriptHash(m.Name, v.OS)
	return err == nil && body == applied.ScriptHash
}

// dependencyError explains why dependent cannot run: its dependency dep is
// neither running this pass nor already applied. A dep that is a configured
// "manual" recipe was skipped because nobody named it; anything else is a dep
// left out of this run (excluded by ApplyOpts.Only, or not on the VM).
func dependencyError(dependent, dep string, manifests map[string]recipes.Manifest, explicit map[string]bool) error {
	if m, ok := manifests[dep]; ok && m.Run == "manual" && !explicit[dep] {
		return fmt.Errorf("%w: %s depends on %s, which has never been applied (run = manual)", ErrRecipeNotApplicable, dependent, dep)
	}
	return fmt.Errorf("%w: %s depends on %s; add it to --recipe or apply it first", ErrRecipeNotApplicable, dependent, dep)
}

// Recipe is one recipe resolved for a specific OS. In v2, recipes are
// directories with recipe.toml manifests; Name is the recipe name (e.g.,
// "xfce"), not a filename.
type Recipe struct {
	Name        string // recipe name, matches the directory name
	Description string // from recipe.toml
	// Reboot says the guest needs a restart before this recipe's effect is
	// visible. A caller that waits for "reachable" after an apply sees the
	// pre-reboot sshd and reads it as done.
	Reboot bool
	// Depends names recipes that run before this one. Apply orders the run
	// itself, so this is for a caller that reports the plan, not one that
	// sorts.
	Depends []string
	// Runtime is the interpreter the script runs under: "sh" or "python3".
	Runtime string
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
			out = append(out, Recipe{
				Name:        m.Name,
				Description: m.Description,
				Reboot:      m.Reboot,
				Depends:     m.Depends,
				Runtime:     m.Runtime,
			})
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
// backendName is ignored in v2: every recipe is a shell script, and the
// backend determines how it runs, not whether it applies (see recipes.List).
// The reason for an inapplicable recipe comes from recipes.MatchReason
// against the recipe's manifest: "docker: built for alpine, not debian".
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
		if !ok[n] {
			issues = append(issues, RecipeIssue{Name: n, Reason: recipeIssueReason(n, osName)})
			continue
		}
		// Applicable, but an install-stage recipe cannot run yet.
		if m, mok, err := recipes.ManifestFor(n); err == nil && mok && m.Stage == "install" {
			issues = append(issues, RecipeIssue{Name: n, Reason: installStageUnsupported})
		}
	}
	return issues, nil
}

// recipeIssueReason explains why name is not offered to osName. A name with no
// recipe.toml is not a recipe. A recipe.toml that fails to parse reports the
// parse error. Otherwise recipes.MatchReason explains the OS or capability
// mismatch that MatchesVM (and so recipes.List) rejected it for.
func recipeIssueReason(name, osName string) string {
	m, ok, err := recipes.ManifestFor(name)
	if err != nil {
		return fmt.Sprintf("%s: %v", name, err)
	}
	if !ok {
		return fmt.Sprintf("no such recipe %q", name)
	}
	if reason := recipes.MatchReason(&m, osName); reason != "" {
		return fmt.Sprintf("%s: %s", name, reason)
	}
	return fmt.Sprintf("%s is not offered to %s", name, osName)
}
