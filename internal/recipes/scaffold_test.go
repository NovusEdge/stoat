package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewWritesAScaffoldThatMatchesTheNamingContract: the filename IS the
// contract, since recipes.List filters on it, so a scaffold that names the file
// wrongly produces a recipe the picker will never offer.
func TestNewWritesAScaffoldThatMatchesTheNamingContract(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	cases := []struct {
		name, osName, backend, wantFile string
	}{
		{"nodejs", "alpine", "", "nodejs.alpine.sh"},
		{"nodejs", "ubuntu", "", "nodejs.ubuntu.sh"},
		{"nodejs", "ubuntu", "cloudinit", "nodejs.cloud.yaml"},
		// An OS the shared cloud fragment cannot serve needs its own file,
		// for the same reason Fedora already has one.
		{"nodejs", "fedora", "cloudinit", "nodejs.fedora.cloud.yaml"},
	}
	for _, c := range cases {
		t.Setenv("STOAT_HOME", t.TempDir())
		path, err := New(c.name, c.osName, c.backend)
		if err != nil {
			t.Fatalf("New(%q,%q,%q): %v", c.name, c.osName, c.backend, err)
		}
		if got := filepath.Base(path); got != c.wantFile {
			t.Errorf("New(%q,%q,%q) wrote %q, want %q", c.name, c.osName, c.backend, got, c.wantFile)
		}

		// And the picker must actually offer what was just written.
		if c.backend == "" {
			names, err := List(c.osName, "apkovl")
			if err != nil {
				t.Fatal(err)
			}
			if !contains(names, c.wantFile) {
				t.Errorf("List(%q, apkovl) does not offer the scaffold %q: %v", c.osName, c.wantFile, names)
			}
		}
	}
}

// TestNewShellScaffoldCarriesTheHonestyBlock: every bundled shell recipe is
// required by this package's own tests to detect a live root and say that
// nothing survives a reboot. A scaffold that omitted it would teach the
// opposite of what the suite enforces.
func TestNewShellScaffoldCarriesTheHonestyBlock(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	path, err := New("thing", "alpine", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)

	for _, want := range []string{"/proc/mounts", "tmpfs", "case ", "esac", "NOT", "set -e", "#!/bin/sh"} {
		if !strings.Contains(body, want) {
			t.Errorf("scaffold is missing %q:\n%s", want, body)
		}
	}
	// Alpine's community repo is off by default, which is where most of what
	// anyone would install actually lives.
	if !strings.Contains(body, "setup-apkrepos -c") {
		t.Error("alpine scaffold does not enable the community repo")
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("shell scaffold is not executable: %v", fi.Mode())
	}
}

// TestNewRefusesToClobber: Install() already promises never to overwrite a
// user's edits. A scaffold command that could destroy the recipe you have
// been working on would be a worse failure than not existing.
func TestNewRefusesToClobber(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	path, err := New("thing", "alpine", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := New("thing", "alpine", ""); err == nil {
		t.Fatal("New overwrote an existing recipe")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "echo mine") {
		t.Error("the user's recipe was destroyed")
	}
}

func TestNewRejectsBadNames(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	for _, name := range []string{"", "has.dot", "has/slash", "has space"} {
		if _, err := New(name, "alpine", ""); err == nil {
			t.Errorf("New(%q) was accepted; dots and slashes are what separate name from os and extension", name)
		}
	}
	// A shell recipe with no OS has no valid filename at all.
	if _, err := New("thing", "", ""); err == nil {
		t.Error("New accepted a shell recipe with no os")
	}
}

func TestInstalledListsEverything(t *testing.T) {
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
	// Unlike List, this is not filtered by os or backend.
	for _, want := range []string{"xfce.alpine.sh", "xfce.cloud.yaml", "docker.alpine.sh"} {
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
