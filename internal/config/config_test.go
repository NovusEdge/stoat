package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	want := &VM{
		Name:      "alpine-live",
		Mode:      "live",
		ISO:       "isos/alpine-standard-3.24.1-x86_64.iso",
		RAM:       4096,
		CPUs:      4,
		Disk:      "8G",
		Installed: true,
		Share:     "/home/someone/vms",
		SSHPort:   2201,
		Recipes:   []string{"xfce"},
		Dir:       filepath.Join(Root(), "alpine-live"),
	}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load("alpine-live")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestSaveLoadRoundtripCloudVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	want := &VM{
		Name:    "ubuntu-cloud",
		Mode:    "cloud",
		OS:      "ubuntu-24.04",
		RAM:     2048,
		CPUs:    2,
		SSHPort: 2202,
		Backend: "cloudinit",
		Base:    "/home/someone/.stoat/base/ubuntu-24.04.qcow2",
		SSHUser: "ubuntu",
		Dir:     filepath.Join(Root(), "ubuntu-cloud"),
	}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load("ubuntu-cloud")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestLoadPreexistingVMTomlHasNoNewFields simulates a vm.toml written before
// this phase: no os/backend/base/sshuser keys at all. It must still load
// cleanly, leaving Backend empty (dispatch elsewhere keys off Mode, not
// this field) and SSHUser empty (defaults to root elsewhere).
func TestLoadPreexistingVMTomlHasNoNewFields(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	writeRawVMToml(t, "alpine-old", `name = "alpine-old"
mode = "live"
iso = "isos/alpine-standard-3.24.1-x86_64.iso"
ram = 4096
cpus = 4
sshport = 2201
`)

	v, err := Load("alpine-old")
	if err != nil {
		t.Fatal(err)
	}
	if v.Backend != "" {
		t.Errorf("pre-phase VM should have empty Backend, got %q", v.Backend)
	}
	if v.SSHUser != "" {
		t.Errorf("pre-phase VM should have empty SSHUser (defaults to root elsewhere), got %q", v.SSHUser)
	}
}

func TestListSorted(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"zeta", "alpha"} {
		v := &VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, Dir: filepath.Join(Root(), n)}
		if err := v.Save(); err != nil {
			t.Fatal(err)
		}
	}
	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 || vms[0].Name != "alpha" || vms[1].Name != "zeta" {
		t.Errorf("want [alpha zeta], got %v", vms)
	}
}

func TestFreePortSkipsClaimedPort(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	v := &VM{Name: "claimed", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Dir: filepath.Join(Root(), "claimed")}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p == 2200 {
		t.Errorf("FreePort returned 2200 even though it is claimed by %q", v.Name)
	}
}

func TestFreePortNoCollisionAcrossSequentialCreates(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	p1, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	v1 := &VM{Name: "first", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: p1, Dir: filepath.Join(Root(), "first")}
	if err := v1.Save(); err != nil {
		t.Fatal(err)
	}

	p2, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p2 == p1 {
		t.Errorf("second FreePort() call returned the same port as the first: %d", p1)
	}
}

// writeRawVMToml writes vm.toml content directly, bypassing Save/toml
// encoding, so a deliberately malformed file can be produced.
func writeRawVMToml(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(Root(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListBrokenReportsMalformedTOML(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	// The user's real-world typo: an unterminated string.
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")

	broken, err := ListBroken()
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 1 || broken[0].Name != "hosed" || broken[0].Err == nil {
		t.Fatalf("want one broken VM named %q with a non-nil error, got %+v", "hosed", broken)
	}

	// It must also disappear from List() rather than crash the caller.
	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 0 {
		t.Errorf("List() should omit the broken VM, got %v", vms)
	}
}

func TestListBrokenSkipsDirectoryWithNoVMToml(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(Root(), "stray"), 0o755); err != nil {
		t.Fatal(err)
	}

	broken, err := ListBroken()
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Errorf("a directory with no vm.toml is not a VM and must not be reported as broken, got %+v", broken)
	}
}

func TestListBrokenSkipsFixedSubdirs(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	// isos/ and recipes/ are not VM directories even if something inside
	// them happens to be named vm.toml.
	for _, d := range []string{"isos", "recipes"} {
		if err := os.WriteFile(filepath.Join(Root(), d, "vm.toml"), []byte("mode = \"disk\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	broken, err := ListBroken()
	if err != nil {
		t.Fatal(err)
	}
	if len(broken) != 0 {
		t.Errorf("isos/ and recipes/ must never be reported as broken, got %+v", broken)
	}
}

func TestFreePortAvoidsPortClaimedByBrokenVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	// A broken vm.toml that nonetheless still has a readable sshport line,
	// holding port 2200 exactly like the user's real second VM did.
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\nsshport = 2200\n")

	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p == 2200 {
		t.Errorf("FreePort returned 2200 even though a broken VM's vm.toml claims it")
	}
}

func TestFreePortFreshInstallNoVMs(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	// Deliberately do not call EnsureRoot: List() will fail to read the
	// root, and FreePort must still succeed rather than treating that as
	// "every port is claimed".
	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p != 2200 {
		t.Errorf("want first free port 2200 on a fresh install, got %d", p)
	}
}
