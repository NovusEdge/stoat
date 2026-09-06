package hostcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/qemu"
)

// binChecks hardcodes "qemu-system-x86_64" as a literal instead of importing
// internal/qemu.Binary: that package pulls in cloudinit/config/recipes,
// which checks.go must not depend on for one string. The test binary can
// afford the import. This test guards the two names against drifting apart.
func TestFirstBinCheckMatchesQemuBinary(t *testing.T) {
	if got := binChecks[0].name; got != qemu.Binary {
		t.Errorf("binChecks[0].name = %q, want internal/qemu.Binary %q", got, qemu.Binary)
	}
}

// A binary that exists and is executable must be found, and a found check must
// carry no Fix, since the Done screen prints every Fix it is given.
func TestRunChecksFindsBinary(t *testing.T) {
	dir := t.TempDir()
	stubName := "qemu-img"
	if runtime.GOOS == "windows" {
		stubName += ".exe"
	}
	stub := filepath.Join(dir, stubName)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	for _, b := range binChecks {
		if b.name != "qemu-img" {
			continue
		}
		c := lookPathCheck(b.name, DistroArch.InstallCmd(b.pkg))
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

// An unknown distro still reports the problem and still names the packages
// to install. It invents no command to run. The test iterates binChecks
// directly, so a nil Fix fails the test instead of being silently skipped.
func TestRunChecksUnknownDistroHasNoCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	for _, b := range binChecks {
		c := lookPathCheck(b.name, DistroUnknown.InstallCmd(b.pkg))
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

func TestProblemsExcludesOptionalFailuresFromReadiness(t *testing.T) {
	cs := []Check{
		{Name: "git", OK: false, Optional: true, Fix: []string{"install git"}},
		{Name: "qemu-img", OK: false, Fix: []string{"install qemu-img"}},
		{Name: "ssh", OK: true, Optional: false},
	}
	got := Problems(cs)
	if len(got) != 1 || got[0].Name != "qemu-img" {
		t.Fatalf("Problems() = %+v, want only required qemu-img failure", got)
	}
}
