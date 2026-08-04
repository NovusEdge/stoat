package recipes

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// writeV2Recipe writes a minimal valid recipe.toml (plus its script) under
// dir()/<name>, exactly the layout ListManifests and install's directory
// path expect (docs/recipe-spec-v2.md).
func writeV2Recipe(t *testing.T, name, description string) {
	t.Helper()
	recipeDir := filepath.Join(dir(), name)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"" + name + "\"\n" +
		"description = \"" + description + "\"\n" +
		"script = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestListManifestsFindsV2Recipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeV2Recipe(t, "xfce", "XFCE desktop")
	writeV2Recipe(t, "docker", "Docker engine")

	got, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ListManifests() = %v, want 2 entries", got)
	}
	// Sorted by name: docker before xfce.
	if got[0].Name != "docker" || got[1].Name != "xfce" {
		t.Errorf("ListManifests() names = [%q, %q], want [docker, xfce]", got[0].Name, got[1].Name)
	}
	if got[1].Description != "XFCE desktop" {
		t.Errorf("xfce Description = %q", got[1].Description)
	}
	if got, err := got[1].ScriptContent("alpine"); err != nil || got != "#!/bin/sh\necho xfce\n" {
		t.Errorf("xfce ScriptContent(alpine) = %q, %v", got, err)
	}
}

func TestListManifestsSkipsDirWithoutManifest(t *testing.T) {
	// A plain subdirectory with no recipe.toml (stray, or someone's
	// scratch dir) is not a v2 recipe and must not be reported, let alone
	// error the whole listing.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(dir(), "not-a-recipe"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeV2Recipe(t, "xfce", "XFCE desktop")

	got, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "xfce" {
		t.Errorf("ListManifests() = %v, want exactly [xfce]", got)
	}
}

func TestListManifestsSkipsInvalidManifestButKeepsOthers(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeV2Recipe(t, "xfce", "XFCE desktop")

	broken := filepath.Join(dir(), "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	// Missing the required "script" field.
	if err := os.WriteFile(filepath.Join(broken, "recipe.toml"), []byte(`name = "broken"`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "xfce" {
		t.Errorf("ListManifests() = %v, want exactly [xfce], with the broken manifest skipped", got)
	}
}

func TestListManifestsNoDirYet(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	got, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("ListManifests() = %v, want nil before Install/anything is written", got)
	}
}

// TestInstallCopiesBundledV2RecipeDirectories exercises install(fs.FS)
// directly against a fake bundled tree, standing in for
// internal/recipes/bundled/<name>/recipe.toml (docs/recipe-spec-v2.md)
// before any real recipe has been migrated to it. It pins that a bundled
// directory entry is walked and mirrored into dir(), keyed by its path
// ("xfce/recipe.toml"), with scripts landing executable.
func TestInstallCopiesBundledV2RecipeDirectories(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	fake := fstest.MapFS{
		"xfce/recipe.toml": {Data: []byte("name = \"xfce\"\nscript = \"install.sh\"\n"), Mode: 0o644},
		"xfce/install.sh":  {Data: []byte("#!/bin/sh\necho xfce\n"), Mode: 0o644},
	}
	if err := install(fake); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir(), "xfce", "recipe.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(fake["xfce/recipe.toml"].Data) {
		t.Errorf("installed recipe.toml = %q, want %q", got, fake["xfce/recipe.toml"].Data)
	}

	info, err := os.Stat(filepath.Join(dir(), "xfce", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("install.sh was not installed executable")
	}

	manifests, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 || manifests[0].Name != "xfce" {
		t.Errorf("ListManifests() after install() = %v, want exactly [xfce]", manifests)
	}
}

// TestInstallPreservesEditsInsideAV2RecipeDirectory mirrors
// TestInstallCopiesBundledRecipesAndPreservesEdits for the directory case: a
// hand-edited script inside an installed recipe directory must survive a
// later install(), the same "never clobber a local edit" rule Install
// already gives flat files.
func TestInstallPreservesEditsInsideAV2RecipeDirectory(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	fake := fstest.MapFS{
		"xfce/recipe.toml": {Data: []byte("name = \"xfce\"\nscript = \"install.sh\"\n"), Mode: 0o644},
		"xfce/install.sh":  {Data: []byte("#!/bin/sh\necho xfce\n"), Mode: 0o644},
	}
	if err := install(fake); err != nil {
		t.Fatal(err)
	}

	edited := "#!/bin/sh\necho mine\n"
	scriptPath := filepath.Join(dir(), "xfce", "install.sh")
	if err := os.WriteFile(scriptPath, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := install(fake); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != edited {
		t.Error("install() overwrote a user edit inside a v2 recipe directory")
	}
}
