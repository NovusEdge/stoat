package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCopiesV2Recipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	// v2 recipes live in subdirectories with recipe.toml
	for _, name := range []string{"xfce", "docker", "devtools", "tailscale"} {
		toml := filepath.Join(dir(), name, "recipe.toml")
		if _, err := os.Stat(toml); err != nil {
			t.Errorf("Install did not install %s/recipe.toml: %v", name, err)
		}
	}
}

func TestInstallPreservesUserEdits(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	// Edit a script inside a recipe directory
	scriptPath := filepath.Join(dir(), "xfce", "install.sh")
	edited := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(scriptPath, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != edited {
		t.Error("Install overwrote a user-edited recipe script")
	}
}

func TestInstallRefreshesUnmodifiedCopies(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	tomlPath := filepath.Join(dir(), "xfce", "recipe.toml")
	fresh, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an older stoat's copy by overwriting AND updating the manifest
	stale := "name = \"xfce\"\nscript = \"old.sh\"\n"
	if err := os.WriteFile(tomlPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	m := readManifest()
	m["xfce/recipe.toml"] = sum([]byte(stale))
	if err := writeManifest(m); err != nil {
		t.Fatal(err)
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fresh) {
		t.Error("Install did not refresh its own stale copy")
	}
}

func TestEmbedContainsBundledDirectory(t *testing.T) {
	entries, err := bundled.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "bundled" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("embedded = %v, want exactly [bundled]", names)
	}

	// Check bundled contains expected recipe directories
	recipes, err := bundled.ReadDir("bundled")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"xfce": true, "docker": true, "devtools": true, "tailscale": true}
	for _, e := range recipes {
		if !e.IsDir() {
			continue
		}
		if !want[e.Name()] {
			t.Errorf("unexpected bundled recipe %q", e.Name())
		}
		delete(want, e.Name())
	}
	for missing := range want {
		t.Errorf("bundled missing recipe %q", missing)
	}
}

func TestListFiltersbyOS(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	contains := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	// Alpine should get all Alpine-compatible recipes
	alpineRecipes, err := List("alpine", "apkovl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"xfce", "docker", "devtools", "tailscale"} {
		if !contains(alpineRecipes, want) {
			t.Errorf("List(alpine) missing %q, got %v", want, alpineRecipes)
		}
	}

	// Ubuntu should get xfce (has ubuntu in OS list) but not alpine-only recipes
	ubuntuRecipes, err := List("ubuntu", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(ubuntuRecipes, "xfce") {
		t.Errorf("List(ubuntu) missing xfce, got %v", ubuntuRecipes)
	}
	// docker/devtools/tailscale are alpine-only
	for _, notWant := range []string{"docker", "devtools", "tailscale"} {
		if contains(ubuntuRecipes, notWant) {
			t.Errorf("List(ubuntu) should not contain %q (alpine-only)", notWant)
		}
	}
}

func TestListReturnsRecipeNames(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	names, err := List("alpine", "apkovl")
	if err != nil {
		t.Fatal(err)
	}

	// Names should be the recipe name (e.g., "xfce"), not filenames
	for _, n := range names {
		if strings.Contains(n, ".sh") || strings.Contains(n, ".yaml") {
			t.Errorf("List returned filename %q, want recipe name", n)
		}
		if strings.Contains(n, "/") {
			t.Errorf("List returned path %q, want recipe name", n)
		}
	}
}

func TestRecipeScriptsHaveLiveVsDiskCheck(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	manifests, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range manifests {
		content, err := m.ScriptContent("alpine")
		if err != nil {
			continue // OS override may not exist
		}

		// Each recipe should detect live vs disk
		if !strings.Contains(content, "/proc/mounts") {
			t.Errorf("%s script does not check /proc/mounts for live vs disk detection", m.Name)
		}
	}
}

func TestRecipeScriptsSetE(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	manifests, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range manifests {
		content, err := m.ScriptContent("alpine")
		if err != nil {
			continue
		}

		if !strings.Contains(content, "set -e") {
			t.Errorf("%s script does not set -e for fail-fast behavior", m.Name)
		}
	}
}

func TestBookkeepingFilesNotListedAsRecipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	// Create a .bak file
	if err := os.WriteFile(filepath.Join(dir(), "stray.bak"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := List("alpine", "apkovl")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".bak") || n == ManifestName {
			t.Errorf("List offered bookkeeping file %q", n)
		}
	}
}
