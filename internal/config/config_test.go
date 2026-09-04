package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadWarnsUnknownKeyToStderr(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "w")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "vm.toml"), []byte("name = \"w\"\nmode = \"live\"\ncpus_ = 2\n"), 0o644)
	var w bytes.Buffer
	UnknownKeyWriter = &w
	t.Cleanup(func() { UnknownKeyWriter = os.Stderr })
	v, err := Load("w")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "w" || !strings.Contains(w.String(), `unknown key "cpus_"`) {
		t.Errorf("v=%+v warn=%q", v, w.String())
	}
}

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
		Forwards:  []PortForward{{HostPort: 8080, GuestPort: 80}},
		Display:   "window",
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

// TestSaveLoadRoundtripEmptyDisplayMeansAuto covers a vm.toml with no display
// preference, the shape every pre-existing VM has. Load must leave it "",
// not default it to a literal "auto": core.validateDisplay and
// qemu.DisplayKind both already treat "" the same as "auto".
func TestSaveLoadRoundtripEmptyDisplayMeansAuto(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	want := &VM{Name: "plain", Mode: "live", SSHPort: 2203, Dir: filepath.Join(Root(), "plain")}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load("plain")
	if err != nil {
		t.Fatal(err)
	}
	if got.Display != "" {
		t.Errorf("Display = %q, want empty", got.Display)
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

// TestSaveLoadRoundtripApplied verifies Applied entries survive a save/load
// cycle, keyed by recipe name.
func TestSaveLoadRoundtripApplied(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	want := &VM{
		Name:    "alpine-live",
		Mode:    "live",
		SSHPort: 2201,
		Applied: map[string]AppliedRecipe{
			"xfce": {Version: "1.2.3", At: at},
		},
		Dir: filepath.Join(Root(), "alpine-live"),
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

// TestLoadPreexistingVMTomlHasNoNewFields simulates a vm.toml written before
// this phase: no os/backend/base/sshuser keys at all. It must still load
// cleanly. Backend stays empty (dispatch elsewhere keys off Mode, not this
// field). SSHUser stays empty (defaults to root elsewhere).
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
	if len(v.Forwards) != 0 {
		t.Errorf("pre-phase VM should have no port forwards, got %v", v.Forwards)
	}
}

// TestLoadPreexistingVMTomlAllowExecDefaultsTrue pins the regression the
// AllowExec field itself warns about. Go's zero value for a bool is false.
// A naive field addition would silently disable exec on every VM whose
// vm.toml predates "allow_exec". Load must correct that.
func TestLoadPreexistingVMTomlAllowExecDefaultsTrue(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	writeRawVMToml(t, "alpine-old", `name = "alpine-old"
mode = "live"
ram = 4096
cpus = 4
sshport = 2201
`)

	v, err := Load("alpine-old")
	if err != nil {
		t.Fatal(err)
	}
	if !v.AllowExec {
		t.Errorf("pre-allow_exec VM should default AllowExec to true, got false")
	}
}

// TestLoadAllowExecExplicitFalseIsHonoured makes sure the default-true
// correction only fires for an ABSENT key, not for one deliberately set to
// false.
func TestLoadAllowExecExplicitFalseIsHonoured(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	writeRawVMToml(t, "locked-down", `name = "locked-down"
mode = "live"
ram = 4096
cpus = 4
sshport = 2201
allow_exec = false
`)

	v, err := Load("locked-down")
	if err != nil {
		t.Fatal(err)
	}
	if v.AllowExec {
		t.Errorf("explicit allow_exec = false should be honoured, got true")
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
	// FreePort probes the host by binding, so anything already listening
	// on 2200 legitimately pushes the answer up. Asserting exactly 2200
	// would fail whenever the developer already had a VM running, which
	// says nothing about FreePort. What matters: a fresh install gets a
	// usable port in range, and that port is genuinely bindable.
	if p < 2200 || p >= 2300 {
		t.Errorf("free port %d is outside the 2200-2299 range", p)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		t.Errorf("FreePort returned %d, which is not actually free: %v", p, err)
	} else {
		_ = l.Close()
	}
}

// The 9p work share lives outside the VM directory, so deleting the VM
// directory alone would leak it.
func TestDeleteRemovesTheWorkShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &VM{Name: "w", Dir: filepath.Join(root, "w")}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(v.WorkDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.WorkDir(), "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v.WorkDir()); !os.IsNotExist(err) {
		t.Error("work share left behind after Delete")
	}
}
