package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCreatesRecipeDirectory(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	path, err := New("nodejs", "alpine", "")
	if err != nil {
		t.Fatalf("New(nodejs, alpine): %v", err)
	}

	// Should create a directory, not a file
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Errorf("New returned %q which is not a directory", path)
	}

	// Should contain recipe.toml and install.sh
	if _, err := os.Stat(filepath.Join(path, "recipe.toml")); err != nil {
		t.Error("recipe.toml not created")
	}
	if _, err := os.Stat(filepath.Join(path, "install.sh")); err != nil {
		t.Error("install.sh not created")
	}

	// The recipe should be listed
	names, err := List("alpine", "apkovl")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(names, "nodejs") {
		t.Errorf("List(alpine) does not offer the new recipe: %v", names)
	}
}

func TestNewShellScaffoldCarriesTheHonestyBlock(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	path, err := New("thing", "alpine", "")
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(path, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)

	for _, want := range []string{"/proc/mounts", "tmpfs", "case ", "esac", "NOT", "set -e", "#!/bin/sh"} {
		if !strings.Contains(body, want) {
			t.Errorf("scaffold is missing %q", want)
		}
	}
	// Alpine's community repo is off by default
	if !strings.Contains(body, "setup-apkrepos -c") {
		t.Error("alpine scaffold does not enable the community repo")
	}

	fi, err := os.Stat(filepath.Join(path, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("shell scaffold is not executable: %v", fi.Mode())
	}
}

func TestNewRefusesToClobber(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	path, err := New("thing", "alpine", "")
	if err != nil {
		t.Fatal(err)
	}

	// Edit the script
	scriptPath := filepath.Join(path, "install.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Try to create again
	if _, err := New("thing", "alpine", ""); err == nil {
		t.Fatal("New overwrote an existing recipe")
	}

	b, _ := os.ReadFile(scriptPath)
	if !strings.Contains(string(b), "echo mine") {
		t.Error("the user's recipe was destroyed")
	}
}

func TestNewRejectsBadNames(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	for _, name := range []string{"", "has.dot", "has/slash", "has space"} {
		if _, err := New(name, "alpine", ""); err == nil {
			t.Errorf("New(%q) was accepted; dots and slashes are not allowed", name)
		}
	}
	// A recipe with no OS is rejected
	if _, err := New("thing", "", ""); err == nil {
		t.Error("New accepted a recipe with no os")
	}
}

func TestInstalledListsRecipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if names, err := Installed(); err != nil || len(names) != 0 {
		t.Errorf("Installed() on a fresh root = %v, %v; want empty", names, err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	names, err := Installed()
	if err != nil {
		t.Fatal(err)
	}
	// Should list recipe names, not filenames
	for _, want := range []string{"xfce", "docker", "devtools", "tailscale"} {
		if !contains(names, want) {
			t.Errorf("Installed() missing %q: %v", want, names)
		}
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
