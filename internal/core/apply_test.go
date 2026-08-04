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
		RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"xfce"},
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

// A valid Only subset must pass validation and reach the actual run,
// proven without a real sshd or the full sshx.WaitTimeout (90s): a bare TCP
// listener that answers with the "SSH-" banner satisfies sshx.Wait's
// reachability check (bannerReady) almost instantly, getting Provision past
// "waiting for ssh" and into its per-recipe ctx.Err() check, where an
// ALREADY-CANCELLED ctx returns context.Canceled immediately. That proves
// two things at once: Only accepted "a" (no ErrRecipeNotApplicable, a
// rejection would have returned before ctx was ever consulted), and the
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
// alpine-only) falls back to the structural reason: ReadMetadata cannot
// read a v2 recipe directory as a flat file, so it has nothing to say, and
// CheckRecipes must still produce a real, useful reason rather than an
// empty one.
func TestCheckRecipesFallsBackToStructuralReason(t *testing.T) {
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
	want := "docker is not offered to debian/apkovl"
	if !strings.Contains(issues[0].Reason, want) {
		t.Errorf("Reason = %q, want it to contain %q (the structural fallback)", issues[0].Reason, want)
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
