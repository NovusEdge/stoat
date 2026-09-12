package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// writeSettings puts a [limits] table in the data root's config.toml.
func writeSettings(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(settings.Path(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func saveVM(t *testing.T, name string, ram int) *config.VM {
	t.Helper()
	v := &config.VM{Name: name, Mode: "live", RAM: ram, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckCreateRefusesAtTheVMLimit(t *testing.T) {
	root(t)
	writeSettings(t, "[limits]\nmax_vms = 2\n")
	saveVM(t, "one", 1024)
	saveVM(t, "two", 1024)

	err := CheckCreate("")
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("CheckCreate = %v, want ErrLimit", err)
	}
	if !strings.Contains(err.Error(), "max_vms is 2") {
		t.Errorf("error = %q, want the configured limit", err)
	}
}

func TestCheckCreateAllowsBelowTheLimit(t *testing.T) {
	root(t)
	writeSettings(t, "[limits]\nmax_vms = 2\n")
	saveVM(t, "one", 1024)

	if err := CheckCreate(""); err != nil {
		t.Fatalf("CheckCreate = %v, want nil", err)
	}
}

func TestNoLimitsMeansNoRefusal(t *testing.T) {
	root(t)
	for _, n := range []string{"one", "two", "three"} {
		saveVM(t, n, 4096)
	}
	if err := CheckCreate(""); err != nil {
		t.Fatalf("CheckCreate = %v, want nil with no [limits] table", err)
	}
}

// A project may lower an account limit. It may not raise one: stoat.toml sits
// in the repository, where an agent can write it.
func TestProjectLimitsOnlyLower(t *testing.T) {
	root(t)
	writeSettings(t, "[limits]\nmax_vms = 2\n")
	dir := t.TempDir()
	toml := "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[limits]\nmax_vms = 9\n\n[vms.dev]\nimage = \"alpine-virt\"\n"
	if err := os.WriteFile(filepath.Join(dir, "stoat.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	saveVM(t, "one", 1024)
	saveVM(t, "two", 1024)

	if err := CheckCreate(dir); !errors.Is(err, ErrLimit) {
		t.Fatalf("CheckCreate = %v, want ErrLimit: a project must not raise max_vms", err)
	}

	lower := "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[limits]\nmax_vms = 1\n\n[vms.dev]\nimage = \"alpine-virt\"\n"
	if err := os.WriteFile(filepath.Join(dir, "stoat.toml"), []byte(lower), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckCreate(dir); !errors.Is(err, ErrLimit) {
		t.Fatalf("CheckCreate = %v, want ErrLimit from the project's own lower limit", err)
	}
}

// A gce instance runs on Google's hardware, so the host's RAM says nothing
// about it.
func TestCheckStartIgnoresGCE(t *testing.T) {
	root(t)
	writeSettings(t, "[limits]\nmax_ram_mb = 1\n")
	v := &config.VM{Name: "cloud", Mode: "live", RAM: 8192, CPUs: 1, Provider: "gce"}
	if err := CheckStart(v, false); err != nil {
		t.Fatalf("CheckStart = %v, want nil for a gce VM", err)
	}
}

func TestTightenTakesTheLowerOfEachField(t *testing.T) {
	account := settings.Limits{MaxVMs: 5, MaxRAMMB: 8192}
	for _, tc := range []struct {
		name    string
		project settings.Limits
		want    settings.Limits
	}{
		{"lower wins", settings.Limits{MaxVMs: 2}, settings.Limits{MaxVMs: 2, MaxRAMMB: 8192}},
		{"higher is ignored", settings.Limits{MaxVMs: 99}, settings.Limits{MaxVMs: 5, MaxRAMMB: 8192}},
		{"unset keeps the account value", settings.Limits{}, account},
		{"a project may set what the account left open", settings.Limits{MaxRAMMB: 1024}, settings.Limits{MaxVMs: 5, MaxRAMMB: 1024}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := account.Tighten(tc.project); got != tc.want {
				t.Errorf("Tighten = %+v, want %+v", got, tc.want)
			}
		})
	}
}
