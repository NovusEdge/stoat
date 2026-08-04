package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// A cloudinit VM's recipes ran from the cloud-init seed at first boot;
// there is no post-boot ssh step for Apply to drive, and running one would
// mean piping a YAML fragment to `sh -s`. Apply must refuse this outright
// rather than attempt it.
func TestApplyRefusesCloudinitBackend(t *testing.T) {
	dir := root(t)
	v := &config.VM{
		Name: "cl", Mode: "cloud", OS: "debian", Backend: "cloudinit",
		RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"xfce.cloud.yaml"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/cl"
	stop := fakeRunning(t, v)
	defer stop()

	if err := Apply(context.Background(), "cl", ApplyOpts{}); !errors.Is(err, ErrAppliedAtBoot) {
		t.Fatalf("err = %v, want ErrAppliedAtBoot", err)
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
		RAM: 512, CPUs: 1, SSHPort: 2200, Recipes: []string{"a.alpine.sh"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	err := Apply(context.Background(), "work", ApplyOpts{Only: []string{"b.alpine.sh"}})
	if !errors.Is(err, ErrRecipeNotApplicable) {
		t.Fatalf("err = %v, want ErrRecipeNotApplicable", err)
	}
}

// A valid Only subset must pass validation and reach the actual run,
// proven without a real sshd or the full sshx.WaitTimeout (90s): a bare TCP
// listener that answers with the "SSH-" banner satisfies sshx.Wait's
// reachability check (bannerReady) almost instantly, getting Provision past
// "waiting for ssh" and into its per-recipe ctx.Err() check, where an
// ALREADY-CANCELLED ctx returns context.Canceled immediately. That proves
// two things at once: Only accepted "a.alpine.sh" (no ErrRecipeNotApplicable,
// a rejection would have returned before ctx was ever consulted), and the
// call reached sshx.Provision's own ctx-aware cancellation, not a second,
// separate one.
func TestApplyOnlyAcceptsAValidSubset(t *testing.T) {
	dir := root(t)

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
		Recipes: []string{"a.alpine.sh", "b.alpine.sh"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Apply(ctx, "work", ApplyOpts{Only: []string{"a.alpine.sh"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (proves Only validated and the run reached sshx.Provision)", err)
	}
}

func TestRecipesRequiresBothOSAndBackend(t *testing.T) {
	root(t)
	if _, err := Recipes(RecipeFilter{OS: "alpine"}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Recipes with no Backend: err = %v, want ErrInvalidSpec", err)
	}
	if _, err := Recipes(RecipeFilter{Backend: "apkovl"}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Recipes with no OS: err = %v, want ErrInvalidSpec", err)
	}
}

// The whole point of Recipes over recipes.List: a caller gets more than a
// bare filename back. Label, TargetOS and Shared are all derived; see
// apply.go's Recipe doc comment for what is deliberately NOT here yet
// (Description/Requires/Stages) and why.
func TestRecipesReturnsMetadataNotJustNames(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	got, err := Recipes(RecipeFilter{OS: "alpine", Backend: "apkovl"})
	if err != nil {
		t.Fatal(err)
	}
	var xfce *Recipe
	for i := range got {
		if got[i].Name == "xfce.alpine.sh" {
			xfce = &got[i]
		}
	}
	if xfce == nil {
		t.Fatalf("xfce.alpine.sh not in %+v", got)
	}
	if xfce.Label != "xfce" {
		t.Errorf("Label = %q, want %q", xfce.Label, "xfce")
	}
	if xfce.TargetOS != "alpine" {
		t.Errorf("TargetOS = %q, want %q", xfce.TargetOS, "alpine")
	}
	if xfce.Shared {
		t.Errorf("Shared = true for an OS-pinned shell recipe")
	}
}

// A shared cloud-config fragment (no OS segment in its filename) must come
// back with Shared true and no TargetOS: the metadata that tells a caller
// apart a fragment meant for one specific OS from one meant for every
// CloudRecipes-eligible OS.
func TestRecipesMarksSharedCloudFragments(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	got, err := Recipes(RecipeFilter{OS: "arch", Backend: "cloudinit"})
	if err != nil {
		t.Fatal(err)
	}
	var devtools *Recipe
	for i := range got {
		if got[i].Name == "devtools.cloud.yaml" {
			devtools = &got[i]
		}
	}
	if devtools == nil {
		t.Fatalf("devtools.cloud.yaml not offered to arch/cloudinit: %+v", got)
	}
	if !devtools.Shared || devtools.TargetOS != "" {
		t.Errorf("devtools.cloud.yaml = %+v, want Shared=true, TargetOS=\"\"", devtools)
	}
}

// A per-OS override must NOT be reported as shared, and must carry its own
// TargetOS; proven against debian's real override, xfce.debian.cloud.yaml.
func TestRecipesPerOSOverrideIsNotShared(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	got, err := Recipes(RecipeFilter{OS: "debian", Backend: "cloudinit"})
	if err != nil {
		t.Fatal(err)
	}
	var xfce *Recipe
	for i := range got {
		if got[i].Name == "xfce.debian.cloud.yaml" {
			xfce = &got[i]
		}
	}
	if xfce == nil {
		t.Fatalf("xfce.debian.cloud.yaml not offered to debian/cloudinit: %+v", got)
	}
	if xfce.Shared {
		t.Errorf("xfce.debian.cloud.yaml reported Shared=true, want false (it's a per-OS override)")
	}
	if xfce.TargetOS != "debian" {
		t.Errorf("TargetOS = %q, want %q", xfce.TargetOS, "debian")
	}
	// And the shared fragment it overrides must not ALSO be offered:
	// recipes.List already suppresses it; Recipes must not add it back.
	for _, r := range got {
		if r.Name == "xfce.cloud.yaml" {
			t.Errorf("xfce.cloud.yaml offered to debian alongside its own override: %+v", got)
		}
	}
}

// The genuinely inapplicable pairing the brief calls out: xfce.cloud.yaml
// (the SHARED fragment) explicitly requested for alpine, which has its own
// xfce.alpine.cloud.yaml and so never gets the shared one from List. The
// reason must say WHY, not just that it's unavailable.
func TestCheckRecipesExplainsAlpineOverride(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "cloudinit", []string{"xfce.cloud.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Name != "xfce.cloud.yaml" {
		t.Errorf("Name = %q, want xfce.cloud.yaml", issues[0].Name)
	}
	if !strings.Contains(issues[0].Reason, "xfce.alpine.cloud.yaml") {
		t.Errorf("Reason = %q, does not name the override that supersedes it", issues[0].Reason)
	}
}

// The same shape of failure, on debian's own override: a second, distinct
// OS hitting the identical code path, not a coincidence special-cased for
// alpine alone.
func TestCheckRecipesExplainsDebianOverride(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("debian", "cloudinit", []string{"xfce.cloud.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if !strings.Contains(issues[0].Reason, "xfce.debian.cloud.yaml") {
		t.Errorf("Reason = %q, does not name the override that supersedes it", issues[0].Reason)
	}
}

func TestCheckRecipesNoSuchRecipe(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("alpine", "apkovl", []string{"not-a-real-recipe.sh"})
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

	issues, err := CheckRecipes("alpine", "apkovl", []string{"xfce.alpine.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none for an applicable recipe", issues)
	}
}

// A shell recipe named for the cloudinit backend is a real, distinct
// mistake from an OS mismatch, worth its own reason.
func TestCheckRecipesShellRecipeOnCloudinitBackend(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("debian", "cloudinit", []string{"xfce.debian.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if !strings.Contains(issues[0].Reason, "cloudinit") {
		t.Errorf("Reason = %q, does not explain the backend mismatch", issues[0].Reason)
	}
}

// writeRecipe drops a recipe file straight into root's recipes/ dir,
// bypassing recipes.Install/the bundled set, so a test can pin exact front
// matter without editing a shipped recipe.
func writeRecipe(t *testing.T, dir, name, body string) {
	t.Helper()
	recipesDir := filepath.Join(dir, "recipes")
	if err := os.MkdirAll(recipesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipesDir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestCheckRecipesUsesDeclaredCapabilityReason is the case this change was
// for: a recipe declaring "requires: systemd" with no "os" restriction of
// its own must produce docs/design/core-api.md §4's exact example,
// "requires systemd, alpine uses openrc", drawn from recipes.UnsupportedReason
// against the recipe's OWN declared metadata, not the generic "not offered
// to alpine/apkovl" the structural fallback would give for the same file.
//
// The filename still pins gizmo.debian.sh to debian (so recipes.List
// excludes it for alpine, same as any other recipe), but the front matter
// deliberately omits "stoat:os": that is what isolates the capability check
// from the OS check UnsupportedReason also makes, proving THIS reason came
// from "requires", not from the filename.
func TestCheckRecipesUsesDeclaredCapabilityReason(t *testing.T) {
	dir := root(t)
	writeRecipe(t, dir, "gizmo.debian.sh", "#!/bin/sh\n# stoat:name        gizmo\n# stoat:requires    systemd\nset -e\necho hi\n")

	issues, err := CheckRecipes("alpine", "apkovl", []string{"gizmo.debian.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	want := "requires systemd, alpine uses openrc"
	if !strings.Contains(issues[0].Reason, want) {
		t.Errorf("Reason = %q, want it to contain %q", issues[0].Reason, want)
	}
}

// TestCheckRecipesFallsBackWithNoDeclaredMetadata covers a recipe with no
// front-matter block at all (every *.cloud.yaml fragment today, by design):
// ReadMetadata returns the zero Metadata, UnsupportedReason has nothing to
// say, and CheckRecipes must fall back to the structural reason rather than
// reporting no issue or an empty one. xfce.cloud.yaml requested for fedora
// hits recipes.List's real "Fedora is kept out of the shared set entirely"
// rule (recipes.go), a reason only the structural path can give.
func TestCheckRecipesFallsBackWithNoDeclaredMetadata(t *testing.T) {
	root(t)
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	issues, err := CheckRecipes("fedora", "cloudinit", []string{"xfce.cloud.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	want := "not in the shared cloud-recipe set"
	if !strings.Contains(issues[0].Reason, want) {
		t.Errorf("Reason = %q, want it to contain %q (the structural fallback)", issues[0].Reason, want)
	}
}
