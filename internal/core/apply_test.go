package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

func TestApplyUnknownVM(t *testing.T) {
	root(t)
	if err := Apply(context.Background(), "nope", ApplyOpts{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestApplyStoppedVMIsRefused(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), "work", ApplyOpts{}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

// A second Apply against a VM another run already holds the provision lock
// for must refuse before it reaches ssh, so it never races the first run's
// apk (or other package manager) invocation on the guest.
func TestApplyRefusesWhileLockIsHeld(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 512, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = filepath.Join(dir, "work")
	stop := fakeRunning(t, v)
	defer stop()

	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		WithProvisionLock(v.Dir, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	defer close(release)

	err := Apply(context.Background(), "work", ApplyOpts{})
	if !errors.Is(err, ErrProvisionInProgress) {
		t.Fatalf("err = %v, want ErrProvisionInProgress", err)
	}
}

// A cloudinit VM no longer refuses Apply: item 3 lets it apply over ssh after
// first boot. A cloudinit VM with no recipes is a plain no-op, which proves
// the old ErrAppliedAtBoot refusal is gone and reaches no ssh step.
func TestApplyCloudinitNoRecipesIsANoop(t *testing.T) {
	dir := root(t)
	v := &config.VM{
		Name: "cl", Mode: "cloud", OS: "debian", Backend: "cloudinit",
		RAM: 1024, CPUs: 1, SSHPort: 2200,
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/cl"
	stop := fakeRunning(t, v)
	defer stop()

	if err := Apply(context.Background(), "cl", ApplyOpts{}); err != nil {
		t.Fatalf("err = %v, want nil (no recipes, no refusal)", err)
	}
}

// A VM with nothing to apply is a no-op, not an error; mirrors Create's own
// "no recipes at all stays valid" rule (core_test.go's
// TestCreateRejectsAnUnavailableRecipe).
func TestApplyNoRecipesIsANoop(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 512, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	if err := Apply(context.Background(), "work", ApplyOpts{}); err != nil {
		t.Fatalf("Apply with no recipes = %v, want nil", err)
	}
}

// Only must refuse a name that is not one of the VM's OWN recipes: it
// selects a subset, it does not smuggle in a new recipe nobody vetted
// against this VM's OS/backend.
func TestApplyOnlyRejectsANameNotOnTheVM(t *testing.T) {
	dir := root(t)
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: 2200, Recipes: []string{"a"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	err := Apply(context.Background(), "work", ApplyOpts{Only: []string{"b"}})
	if !errors.Is(err, ErrRecipeNotApplicable) {
		t.Fatalf("err = %v, want ErrRecipeNotApplicable", err)
	}
}

// A valid Only subset must pass validation and reach the actual run. This
// is proven without a real sshd or the full sshx.WaitTimeout (90s): a bare
// TCP listener answering the "SSH-" banner satisfies sshx.Wait's
// reachability check (bannerReady) almost instantly, getting Provision past
// "waiting for ssh" into its per-recipe ctx.Err() check. An
// already-cancelled ctx then returns context.Canceled immediately. That
// proves two things: Only accepted "a" (a rejection would have returned
// before ctx was ever consulted), and the call reached sshx.Provision's own
// ctx-aware cancellation.
func TestApplyOnlyAcceptsAValidSubset(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "a", "always", "1.0", "#!/bin/sh\necho a\n")
	writeV2Recipe(t, dir, "b", "always", "1.0", "#!/bin/sh\necho b\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("SSH-2.0-fake\r\n"))
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port,
		Recipes: []string{"a", "b"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Apply(ctx, "work", ApplyOpts{Only: []string{"a"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (proves Only validated and the run reached sshx.Provision)", err)
	}
}

// Recipes' filter requires OS: without it there is nothing to match a
// recipe.toml's declared "os" list against. Backend is accepted for API
// compatibility only and is not required (v2 recipes are all shell scripts;
// the backend decides how a recipe runs, not whether it applies).
func TestRecipesRequiresOS(t *testing.T) {
	root(t)
	if _, err := Recipes(RecipeFilter{Backend: "apkovl"}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Recipes with no OS: err = %v, want ErrInvalidSpec", err)
	}
}

// The whole point of Recipes over recipes.List: a caller gets more than a
// bare name back. In v2, Recipe is {Name, Description}, both read straight
// from the recipe's recipe.toml.
func TestRecipesReturnsMetadataNotJustNames(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	got, err := Recipes(RecipeFilter{OS: "alpine"})
	if err != nil {
		t.Fatal(err)
	}
	var xfce *Recipe
	for i := range got {
		if got[i].Name == "xfce" {
			xfce = &got[i]
		}
	}
	if xfce == nil {
		t.Fatalf("xfce not in %+v", got)
	}
	if xfce.Description == "" {
		t.Errorf("Description is empty, want the recipe.toml description")
	}
}

func TestCheckRecipesNoSuchRecipe(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "apkovl", []string{"not-a-real-recipe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Reason, "no such recipe") {
		t.Fatalf("issues = %+v, want one \"no such recipe\" issue", issues)
	}
}

// An applicable recipe reports no issue at all: CheckRecipes only ever
// names what's WRONG, so a clean answer is an empty slice, not a slice of
// "OK" entries a caller has to filter.
func TestCheckRecipesOKRecipeReportsNoIssue(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "apkovl", []string{"xfce"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none for an applicable recipe", issues)
	}
}

// A recipe requested for an OS its recipe.toml doesn't declare (docker is
// alpine-only) reports the OS mismatch from recipes.MatchReason.
func TestCheckRecipesReportsOSMismatch(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("debian", "apkovl", []string{"docker"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	want := "docker: built for alpine, not debian"
	if !strings.Contains(issues[0].Reason, want) {
		t.Errorf("Reason = %q, want it to contain %q", issues[0].Reason, want)
	}
}

// writeV2Recipe drops a v2 recipe (recipe.toml + install.sh) straight into
// root's recipes/ dir, mirroring recipes.writeV2Recipe for core's own tests,
// which need to set Run and Version, fields that helper doesn't take.
func writeV2Recipe(t *testing.T, rootDir, name, run, version, script string) {
	t.Helper()
	recipeDir := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"" + name + "\"\n" +
		"version = \"" + version + "\"\n" +
		"script = \"install.sh\"\n" +
		"run = \"" + run + "\"\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeDepRecipe writes a v2 recipe declaring depends, for the dependency
// ordering tests. run sets the run mode; depends is written verbatim as a
// TOML string array.
func writeDepRecipe(t *testing.T, rootDir, name, run string, depends []string) {
	t.Helper()
	recipeDir := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	quoted := make([]string, len(depends))
	for i, d := range depends {
		quoted[i] = "\"" + d + "\""
	}
	toml := "name = \"" + name + "\"\n" +
		"script = \"install.sh\"\n" +
		"run = \"" + run + "\"\n" +
		"depends = [" + strings.Join(quoted, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestPlanApplyOnStoppedVM pins the dry-run plan: it is computed host-side, so
// a stopped VM (never started here) still returns a correct run/skip plan. An
// applied "once" recipe with a matching hash skips; an unapplied one runs.
func TestPlanApplyOnStoppedVM(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "xfce", "once", "1.0", "#!/bin/sh\necho xfce\n")
	writeV2Recipe(t, dir, "docker", "once", "2.0", "#!/bin/sh\necho docker\n")
	xfceHash, err := recipes.ScriptHash("xfce", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: 2200,
		Recipes: []string{"xfce", "docker"},
		Applied: map[string]config.AppliedRecipe{"xfce": {Version: "1.0", Hash: xfceHash}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanApply("work", ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 {
		t.Fatalf("plan = %+v, want 2 entries", plan)
	}
	byName := map[string]ApplyPlan{}
	for _, p := range plan {
		byName[p.Name] = p
	}
	if byName["xfce"].Action != "skip" || byName["xfce"].Reason != "already applied" || byName["xfce"].Version != "1.0" {
		t.Errorf("xfce = %+v, want skip/already applied at 1.0", byName["xfce"])
	}
	if byName["docker"].Action != "run" || byName["docker"].Reason != "never applied" {
		t.Errorf("docker = %+v, want run/never applied", byName["docker"])
	}
}

// TestCheckRecipesRejectsInstallStage pins item 6: an install-stage recipe is
// applicable but refused at add time, since no backend runs one yet.
func TestCheckRecipesRejectsInstallStage(t *testing.T) {
	dir := root(t)
	rd := filepath.Join(dir, "recipes", "partitioner")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte("name = \"partitioner\"\nstage = \"install\"\nscript = \"install.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "apkovl", []string{"partitioner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Reason, "install-stage recipes are not yet supported") {
		t.Fatalf("issues = %+v, want one install-stage rejection", issues)
	}
}

// TestFilterByRunModeOrdersDependenciesFirst pins that a dependency runs
// before its dependent regardless of the order in v.Recipes.
func TestFilterByRunModeOrdersDependenciesFirst(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "docker", "always", nil)
	writeDepRecipe(t, dir, "devtools", "always", []string{"docker"})

	v := &config.VM{OS: "alpine"}
	// devtools listed first, but docker must still run first.
	kept, _, err := filterByRunMode(v, []string{"devtools", "docker"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0] != "docker" || kept[1] != "devtools" {
		t.Errorf("kept = %v, want [docker devtools]", kept)
	}
}

// TestCycleDetectionAtApplyTime catches a cycle introduced by manifests edited
// on disk after add-time: filterByRunMode must error, not fall back to array
// order.
func TestCycleDetectionAtApplyTime(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "a", "always", []string{"b"})
	writeDepRecipe(t, dir, "b", "always", []string{"a"})

	v := &config.VM{OS: "alpine"}
	_, _, err := filterByRunMode(v, []string{"a", "b"}, nil)
	if err == nil || !strings.Contains(err.Error(), "cycle detected") {
		t.Fatalf("err = %v, want a cycle error", err)
	}
}

// TestDependencySatisfaction covers the three rules: a manual, never-applied
// dependency errors; an already-applied dependency satisfies; a dependency
// left out of the run (not on the VM here) errors with the "add it" message.
func TestDependencySatisfaction(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "docker", "manual", nil)
	writeDepRecipe(t, dir, "devtools", "always", []string{"docker"})

	// docker is manual and never named, so it does not run; devtools cannot.
	v := &config.VM{OS: "alpine"}
	_, _, err := filterByRunMode(v, []string{"devtools", "docker"}, nil)
	if err == nil || !strings.Contains(err.Error(), "run = manual") {
		t.Fatalf("err = %v, want the manual-dependency error", err)
	}

	// docker already applied: devtools' dependency is satisfied.
	v.Applied = map[string]config.AppliedRecipe{"docker": {Version: "1.0"}}
	kept, _, err := filterByRunMode(v, []string{"devtools", "docker"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "devtools" {
		t.Errorf("kept = %v, want [devtools] (docker manual+applied stays skipped)", kept)
	}

	// docker is not on the VM at all and not applied: the "add it" error.
	v2 := &config.VM{OS: "alpine"}
	_, _, err = filterByRunMode(v2, []string{"devtools"}, nil)
	if err == nil || !strings.Contains(err.Error(), "add it to --recipe or apply it first") {
		t.Fatalf("err = %v, want the missing-dependency error", err)
	}
}

// TestFilterByRunModeSkipsOnceWithMatchingHash pins the base case: a "once"
// recipe whose stored hash still matches its current script stays skipped,
// even though nothing here checks Version at all.
func TestFilterByRunModeSkipsOnceWithMatchingHash(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		OS: "alpine",
		Applied: map[string]config.AppliedRecipe{
			"tool": {Version: "1.0", Hash: hash},
		},
	}

	kept, _, err := filterByRunMode(v, []string{"tool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Errorf("kept = %v, want none: the stored hash still matches", kept)
	}
}

// TestFilterByRunModeRerunsOnChangedScript is the behavior this feature
// adds: a fixed script at the SAME version must re-run, since a version
// bump is no longer the only signal filterByRunMode trusts.
func TestFilterByRunModeRerunsOnChangedScript(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho fixed\n")
	staleHash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	// Recorded against the OLD script body, before the fix landed.
	v := &config.VM{
		OS: "alpine",
		Applied: map[string]config.AppliedRecipe{
			"tool": {Version: "1.0", Hash: staleHash + "stale"},
		},
	}

	kept, _, err := filterByRunMode(v, []string{"tool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "tool" {
		t.Errorf("kept = %v, want [tool]: the script hash no longer matches", kept)
	}
}

// TestFilterByRunModeRerunsOnEmptyStoredHash covers the pre-existing VM
// case: an Applied entry saved before Hash existed decodes with an empty
// string, which never equals a real hash, so the recipe self-heals once.
func TestFilterByRunModeRerunsOnEmptyStoredHash(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	v := &config.VM{
		OS: "alpine",
		Applied: map[string]config.AppliedRecipe{
			"tool": {Version: "1.0"}, // no Hash: predates this field
		},
	}

	kept, _, err := filterByRunMode(v, []string{"tool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "tool" {
		t.Errorf("kept = %v, want [tool]: an empty stored hash never matches", kept)
	}
}

// TestFilterByRunModeAlwaysIgnoresHash pins that "always" keeps running
// regardless of a matching hash: run mode still wins over hash comparison.
func TestFilterByRunModeAlwaysIgnoresHash(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "always", "1.0", "#!/bin/sh\necho one\n")
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		OS: "alpine",
		Applied: map[string]config.AppliedRecipe{
			"tool": {Version: "1.0", Hash: hash},
		},
	}

	kept, _, err := filterByRunMode(v, []string{"tool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "tool" {
		t.Errorf("kept = %v, want [tool]: always never skips", kept)
	}
}

// TestFilterByRunModeManualNeedsExplicit pins that "manual" still needs
// explicit[name], unaffected by hash comparison.
func TestFilterByRunModeManualNeedsExplicit(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "manual", "1.0", "#!/bin/sh\necho one\n")
	v := &config.VM{OS: "alpine"}

	kept, _, err := filterByRunMode(v, []string{"tool"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Errorf("kept = %v, want none: manual needs an explicit name", kept)
	}

	kept, _, err = filterByRunMode(v, []string{"tool"}, map[string]bool{"tool": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "tool" {
		t.Errorf("kept = %v, want [tool]: named explicitly", kept)
	}
}

// TestApplyRecordsHashAndSkipsOnRerun exercises the whole path through
// Apply: a successful run records the script hash, and a second Apply with
// no script change selects nothing to run.
func TestApplyRecordsHashAndSkipsOnRerun(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("SSH-2.0-fake\r\n"))
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"tool"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	// Provision needs a real ssh binary and sshd it will never reach; a
	// cancelled ctx makes it fail fast right after the ssh banner check,
	// before this test needs it to succeed. This test is about the
	// pre/post-run bookkeeping in filterByRunMode and Apply's record step,
	// exercised directly rather than through a full provisioning run.
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	explicit := map[string]bool{}
	kept, manifests, err := filterByRunMode(v, v.Recipes, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %v, want [tool] on the first pass", kept)
	}
	m := manifests["tool"]
	v.Applied = map[string]config.AppliedRecipe{
		"tool": {Version: m.Version, Hash: hash, At: time.Now()},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Applied["tool"].Hash != hash {
		t.Fatalf("saved Hash = %q, want %q", reloaded.Applied["tool"].Hash, hash)
	}

	kept, _, err = filterByRunMode(reloaded, reloaded.Recipes, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Errorf("kept = %v, want none: second pass, no script change", kept)
	}
}

// TestCheckRecipesReportsCapabilityMismatch pins that a recipe declaring
// "requires = [systemd]" with no "os" restriction reports the capability that
// alpine lacks, from recipes.MatchReason. The manifest omits "os"
// deliberately, isolating the capability check from the OS check MatchReason
// makes first: this proves the reason came from "requires", not from "os".
func TestCheckRecipesReportsCapabilityMismatch(t *testing.T) {
	dir := root(t)
	rd := filepath.Join(dir, "recipes", "gizmo")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"gizmo\"\nrequires = [\"systemd\"]\nscript = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "apkovl", []string{"gizmo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	want := "gizmo: requires systemd, which alpine does not have"
	if !strings.Contains(issues[0].Reason, want) {
		t.Errorf("Reason = %q, want it to contain %q", issues[0].Reason, want)
	}
}
