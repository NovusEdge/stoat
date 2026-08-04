package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/qemu"
)

// binChecks hardcodes "qemu-system-x86_64" as a literal rather than
// importing internal/qemu.Binary: that package drags in cloudinit/config/
// recipes, which checks.go has no business depending on for a string. The
// test binary can afford the import that production code can't, so this is
// the guard against the two names drifting apart if stoat ever renames the
// binary it shells out to.
func TestFirstBinCheckMatchesQemuBinary(t *testing.T) {
	if got := binChecks[0].name; got != qemu.Binary {
		t.Errorf("binChecks[0].name = %q, want internal/qemu.Binary %q", got, qemu.Binary)
	}
}

// An empty PATH guarantees exec.LookPath fails for everything, regardless of
// what happens to be installed on the machine running tests. Same trick as
// internal/cloudinit/cloudinit_test.go.
func TestRunChecksAllMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	checks := RunChecks(DistroArch)

	var names []string
	for _, c := range checks {
		names = append(names, c.Name)
	}
	want := []string{"qemu-system-x86_64", "qemu-img", "ssh", "xorriso", "/dev/kvm"}
	if len(names) != len(want) {
		t.Fatalf("got %d checks %v, want %d %v", len(names), names, len(want), want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("check %d = %q, want %q", i, names[i], want[i])
		}
	}

	for _, c := range checks[:4] {
		if c.OK {
			t.Errorf("%s: OK with an empty PATH", c.Name)
		}
		if c.Detail != "not found" {
			t.Errorf("%s: Detail = %q, want %q", c.Name, c.Detail, "not found")
		}
		if len(c.Fix) == 0 {
			t.Errorf("%s: a failed check must carry a Fix", c.Name)
		}
		if !strings.HasPrefix(c.Fix[0], "sudo pacman") {
			t.Errorf("%s: Fix = %v, want an arch command", c.Name, c.Fix)
		}
	}
}

// A binary that exists and is executable must be found, and a found check must
// carry no Fix, since the Done screen prints every Fix it is given.
func TestRunChecksFindsBinary(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "qemu-img")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	for _, c := range RunChecks(DistroArch) {
		if c.Name != "qemu-img" {
			continue
		}
		if !c.OK {
			t.Fatalf("qemu-img not found on a PATH containing it: %+v", c)
		}
		if c.Detail != dir {
			t.Errorf("Detail = %q, want the containing dir %q", c.Detail, dir)
		}
		if len(c.Fix) != 0 {
			t.Errorf("a passing check must not carry a Fix, got %v", c.Fix)
		}
		return
	}
	t.Fatal("no qemu-img check in the list")
}

// An unknown distro still reports the problem, and still names the packages
// to install: it just invents no command to run. Iterating binChecks
// directly (rather than trusting whatever RunChecks(DistroUnknown) returns)
// is what makes this test able to fail: the previous version ranged over a
// nil c.Fix and asserted nothing.
func TestRunChecksUnknownDistroHasNoCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	checks := RunChecks(DistroUnknown)
	for i, b := range binChecks {
		c := checks[i]
		if c.OK {
			t.Fatalf("%s: OK with an empty PATH", c.Name)
		}
		if len(c.Fix) == 0 {
			t.Fatalf("%s: unknown distro lost the package names entirely", c.Name)
		}
		fix := strings.Join(c.Fix, " ")
		if strings.Contains(fix, "sudo pacman") || strings.Contains(fix, "sudo apt") || strings.Contains(fix, "sudo dnf") {
			t.Errorf("%s: invented a package manager command for an unknown distro: %q", c.Name, fix)
		}
		for _, name := range []string{b.pkg.Arch, b.pkg.Debian, b.pkg.Fedora} {
			if !strings.Contains(fix, name) {
				t.Errorf("%s: Fix %q is missing the %s package name", c.Name, fix, name)
			}
		}
	}
}

func TestKVMCheckAt(t *testing.T) {
	dir := t.TempDir()

	writable := filepath.Join(dir, "kvm-ok")
	if err := os.WriteFile(writable, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if c := kvmCheckAt(writable); !c.OK {
		t.Errorf("a read/write file should pass: %+v", c)
	} else if len(c.Fix) != 0 {
		t.Errorf("a passing check must not carry a Fix, got %v", c.Fix)
	}

	missing := filepath.Join(dir, "does-not-exist")
	c := kvmCheckAt(missing)
	if c.OK {
		t.Error("a missing device should fail")
	}
	if !strings.Contains(c.Detail, "not present") {
		t.Errorf("Detail = %q, want it to mention the device is not present", c.Detail)
	}

	denied := filepath.Join(dir, "kvm-denied")
	if err := os.WriteFile(denied, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	// root ignores file permissions, so this case is only meaningful unprivileged.
	if os.Geteuid() != 0 {
		c := kvmCheckAt(denied)
		if c.OK {
			t.Error("an unreadable device should fail")
		}
		if c.Detail != "permission denied" {
			t.Errorf("Detail = %q, want %q", c.Detail, "permission denied")
		}
		if len(c.Fix) == 0 {
			t.Error("a permission failure must carry the usermod fix")
		}
		if !strings.Contains(strings.Join(c.Fix, " "), "usermod -aG kvm") {
			t.Errorf("Fix = %v, want the usermod command", c.Fix)
		}
	}
}

// TestKVMCheckAtOtherError drives kvmCheckAt's default branch: an error that
// is neither fs.ErrNotExist nor fs.ErrPermission. A path whose parent is a
// regular file rather than a directory reliably yields ENOTDIR.
func TestKVMCheckAtOtherError(t *testing.T) {
	dir := t.TempDir()

	notADir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o666); err != nil {
		t.Fatal(err)
	}

	c := kvmCheckAt(filepath.Join(notADir, "kvm"))
	if c.OK {
		t.Error("a path through a non-directory should fail")
	}
	if c.Detail == "" || strings.Contains(c.Detail, "not present") || c.Detail == "permission denied" {
		t.Errorf("Detail = %q, want the underlying error text, not the not-exist/permission-denied cases", c.Detail)
	}
}

func TestProblems(t *testing.T) {
	cs := []Check{
		{Name: "a", OK: true},
		{Name: "b", OK: false},
		{Name: "c", OK: true},
		{Name: "d", OK: false},
	}
	got := Problems(cs)
	if len(got) != 2 || got[0].Name != "b" || got[1].Name != "d" {
		t.Errorf("Problems() = %+v, want the two failures b and d", got)
	}
}
