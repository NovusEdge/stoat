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

// Upgrading from v1 must leave a recipes directory that reads as pure v2.
// Otherwise fifteen dead flat files sit next to four live directories, all
// equally authoritative to someone opening the folder to write a recipe.
func TestInstallSweepsV1FlatFilesAside(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// A representative v1 directory: bundled scripts, a cloud fragment, a
	// .bak left by v1's own installer, and a hand-written recipe.
	v1 := []string{"xfce.alpine.sh", "xfce.cloud.yaml", "xfce.cloud.yaml.bak", "mine.sh"}
	for _, n := range v1 {
		if err := os.WriteFile(filepath.Join(dir(), n), []byte("# v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			t.Errorf("%s survived in the recipes root; v2 recipes are directories", e.Name())
		}
	}
	// Moved, not destroyed: one of them may be the user's own, and no rule
	// here can tell that from something stoat shipped.
	for _, n := range v1 {
		if _, err := os.Stat(filepath.Join(dir(), v1AtticName, n)); err != nil {
			t.Errorf("%s was not preserved in the attic: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir(), "xfce", "recipe.toml")); err != nil {
		t.Errorf("the sweep broke the install itself: %v", err)
	}
}

// The sweep runs on every Install, so it has to be safe to run twice. The
// second pass must not clobber the attic copy with a file the user has since
// re-created under the same name.
func TestInstallSweepIsIdempotent(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir(), "mine.sh"), []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	// Same name reappears with different content, as it would if the user
	// re-created it not realising v1 is gone.
	if err := os.WriteFile(filepath.Join(dir(), "mine.sh"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir(), v1AtticName, "mine.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original\n" {
		t.Errorf("attic holds %q, want the ORIGINAL: the older copy is the one worth keeping", got)
	}
	if _, err := os.Stat(filepath.Join(dir(), "mine.sh")); !os.IsNotExist(err) {
		t.Errorf("the second copy survived in the recipes root")
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

// TestScriptBodyResolvesV2Directory pins the fix for the "is a directory"
// provision failure. A v2 recipe is a directory; reading the name as a file
// returns that error. ScriptBody resolves through the manifest to install.sh.
func TestScriptBodyResolvesV2Directory(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	rd := filepath.Join(dir(), "devtools")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"devtools\"\nscript = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	want := "#!/bin/sh\necho devtools\n"
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte(want), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ScriptBody("devtools", "alpine")
	if err != nil {
		t.Fatalf("ScriptBody: %v", err)
	}
	if got != want {
		t.Errorf("ScriptBody = %q, want the install.sh body %q", got, want)
	}
}

// TestScriptBodyReadsV1FlatFile keeps the old format working: a name with no
// recipe.toml is a v1 flat file, read as-is.
func TestScriptBodyReadsV1FlatFile(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "# v1 legacy\n"
	if err := os.WriteFile(filepath.Join(dir(), "legacy.sh"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScriptBody("legacy.sh", "alpine")
	if err != nil {
		t.Fatalf("ScriptBody: %v", err)
	}
	if got != want {
		t.Errorf("ScriptBody = %q, want %q", got, want)
	}
}

func TestRuntimeForV1FlatFile(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir(), "legacy.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RuntimeFor("legacy.sh", "alpine")
	if err != nil {
		t.Fatalf("RuntimeFor: %v", err)
	}
	if got != "sh" {
		t.Errorf("RuntimeFor(legacy.sh) = %q, want sh", got)
	}
}

func TestRuntimeForManifestPython3(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	rd := filepath.Join(dir(), "pyrecipe")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"pyrecipe\"\nscript = \"install.py\"\nruntime = \"python3\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := RuntimeFor("pyrecipe", "alpine")
	if err != nil {
		t.Fatalf("RuntimeFor: %v", err)
	}
	if got != "python3" {
		t.Errorf("RuntimeFor(pyrecipe) = %q, want python3", got)
	}
}

// TestScriptHashMatchesScriptBody pins ScriptHash to the sha256 of the exact
// bytes ScriptBody resolves, so a caller can compare it against a stored
// AppliedRecipe.Hash without re-deriving the sum itself.
func TestScriptHashMatchesScriptBody(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	rd := filepath.Join(dir(), "devtools")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"devtools\"\nscript = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\necho devtools\n"
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ScriptHash("devtools", "alpine")
	if err != nil {
		t.Fatalf("ScriptHash: %v", err)
	}
	if want := sum([]byte(body)); got != want {
		t.Errorf("ScriptHash = %q, want %q", got, want)
	}
}

// TestScriptHashChangesWithScriptBody proves a fixed script produces a
// different hash than the original, the signal filterByRunMode uses to
// re-apply a "once" recipe whose script changed at the same version.
func TestScriptHashChangesWithScriptBody(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	rd := filepath.Join(dir(), "devtools")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"devtools\"\nscript = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("#!/bin/sh\necho one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := ScriptHash("devtools", "alpine")
	if err != nil {
		t.Fatalf("ScriptHash: %v", err)
	}

	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("#!/bin/sh\necho two\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := ScriptHash("devtools", "alpine")
	if err != nil {
		t.Fatalf("ScriptHash: %v", err)
	}
	if before == after {
		t.Errorf("ScriptHash unchanged after the script body changed: %q", before)
	}
}
