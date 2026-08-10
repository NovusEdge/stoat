package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckDependenciesMissingDep pins the CLI/create rule: a recipe whose
// dependency is not in the list errors, naming what to add.
func TestCheckDependenciesMissingDep(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "docker", "always", nil)
	writeDepRecipe(t, dir, "devtools", "always", []string{"docker"})

	err := CheckDependencies([]string{"devtools"})
	if err == nil || !strings.Contains(err.Error(), "add it to the recipe list") {
		t.Fatalf("err = %v, want the missing-dependency error", err)
	}
	if !errors.Is(err, ErrRecipeNotApplicable) {
		t.Errorf("err = %v, want ErrRecipeNotApplicable", err)
	}

	// Both listed: no error.
	if err := CheckDependencies([]string{"devtools", "docker"}); err != nil {
		t.Errorf("CheckDependencies with both listed: %v", err)
	}
}

// TestCheckDependenciesCycle catches a cycle at create time.
func TestCheckDependenciesCycle(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "a", "always", []string{"b"})
	writeDepRecipe(t, dir, "b", "always", []string{"a"})

	err := CheckDependencies([]string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "cycle detected") {
		t.Fatalf("err = %v, want a cycle error", err)
	}
}

// TestResolveDependenciesAutoAdds pins the TUI helper: a missing dependency
// that applies to the VM's OS comes back to be auto-added.
func TestResolveDependenciesAutoAdds(t *testing.T) {
	dir := root(t)
	writeDepRecipe(t, dir, "docker", "always", nil)
	writeDepRecipe(t, dir, "devtools", "always", []string{"docker"})

	added, err := ResolveDependencies("alpine", []string{"devtools"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Recipe != "docker" || added[0].RequiredBy != "devtools" {
		t.Errorf("added = %v, want [{docker devtools}]", added)
	}

	// docker already present: nothing to add.
	added, err = ResolveDependencies("alpine", []string{"devtools", "docker"})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("added = %v, want none", added)
	}
}

// TestResolveDependenciesRejectsOSMismatch pins that a dependency that does not
// apply to the VM's OS is not silently auto-added; it errors with the reason.
func TestResolveDependenciesRejectsOSMismatch(t *testing.T) {
	dir := root(t)
	rd := filepath.Join(dir, "recipes", "docker")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte("name = \"docker\"\nos = [\"alpine\"]\nscript = \"install.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDepRecipe(t, dir, "devtools", "always", []string{"docker"})

	_, err := ResolveDependencies("debian", []string{"devtools"})
	if err == nil || !strings.Contains(err.Error(), "built for alpine, not debian") {
		t.Fatalf("err = %v, want an OS-mismatch error", err)
	}
}
