package recipes

import (
	"path/filepath"
	"strings"
	"testing"
)

// seedRecipe writes a minimal recipe directory under root.
func seedRecipe(t *testing.T, root, name string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "recipe.toml"),
		"name = \""+name+"\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(root, name, "install.sh"), "set -e\n")
}

func TestRootsOrderAndShadowing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), "[recipes]\n")
	t.Chdir(wd)

	seedRecipe(t, filepath.Join(home, "recipes"), "shared")
	seedRecipe(t, filepath.Join(wd, ".stoat", "recipes"), "shared")
	writeFile(t, filepath.Join(wd, ".stoat", "recipes", "shared", "recipe.toml"),
		"name = \"shared\"\n")
	writeFile(t, filepath.Join(wd, "stoat.lock"), "schema = 1\n[recipes.shared]\nsource = \"s\"\nref = \"main\"\ncommit = \"abc\"\nadded = \"now\"\n")

	path, scope, ok, err := ResolvePath("shared")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("shared did not resolve")
	}
	if scope != "project" {
		t.Errorf("scope = %q, want %q", scope, "project")
	}
	if path != filepath.Join(wd, ".stoat", "recipes", "shared") {
		t.Errorf("path = %q, want the project cache", path)
	}
}

func TestScopeOfLabelsHomeEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	t.Chdir(t.TempDir())

	root := filepath.Join(home, "recipes")
	seedRecipe(t, root, "mine")
	seedRecipe(t, root, "docker")
	seedRecipe(t, root, "tailscale")
	writeFile(t, filepath.Join(root, ManifestName), "0  docker/recipe.toml\n")
	writeFile(t, filepath.Join(home, "stoat.lock"),
		"schema = 1\n[recipes.tailscale]\nsource = \"s\"\nref = \"v1\"\ncommit = \"abc\"\nadded = \"now\"\n")

	for name, want := range map[string]string{"mine": "local", "docker": "bundled", "tailscale": "global"} {
		got, err := ScopeOf(name)
		if err != nil {
			t.Fatalf("ScopeOf(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("ScopeOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestGlobalLockErrorsSurfaceAtResolutionBoundaries(t *testing.T) {
	for name, lock := range map[string]string{
		"newer":   "schema = 2\n",
		"corrupt": "schema = [\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("STOAT_HOME", home)
			t.Chdir(t.TempDir())
			seedRecipe(t, filepath.Join(home, "recipes"), "tailscale")
			writeFile(t, filepath.Join(home, "stoat.lock"), lock)

			_, _, ok, err := ResolvePath("tailscale")
			if err == nil {
				t.Fatalf("ResolvePath error = nil, ok = %v", ok)
			}
			if name == "newer" && !strings.Contains(err.Error(), "schema 2 is newer than this stoat (1)") {
				t.Fatalf("ResolvePath error = %v", err)
			}

			scope, err := ScopeOf("tailscale")
			if err == nil {
				t.Fatalf("ScopeOf error = nil, scope = %q", scope)
			}
			if name == "newer" && !strings.Contains(err.Error(), "schema 2 is newer than this stoat (1)") {
				t.Fatalf("ScopeOf error = %v", err)
			}
		})
	}
}

func TestListManifestsDoesNotFallThroughAnInvalidProjectShadow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), "[recipes]\n")
	t.Chdir(wd)

	seedRecipe(t, filepath.Join(home, "recipes"), "shared")
	seedRecipe(t, filepath.Join(home, "recipes"), "onlyhome")
	seedRecipe(t, filepath.Join(wd, ".stoat", "recipes"), "shared")
	writeFile(t, filepath.Join(wd, ".stoat", "recipes", "shared", "recipe.toml"),
		"name = \"shared\"\n")

	ms, err := ListManifests()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Name != "onlyhome" {
		t.Fatalf("manifests = %d, want only the unshadowed home recipe: %+v", len(ms), ms)
	}
}
