package hostcheck

import (
	"os"
	"path/filepath"
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
	want := []string{"qemu-system-x86_64", "qemu-img", "ssh", "xorriso", "git", "/dev/kvm"}
	if len(names) != len(want) {
		t.Fatalf("got %d checks %v, want %d %v", len(names), names, len(want), want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("check %d = %q, want %q", i, names[i], want[i])
		}
	}

	for _, c := range checks[:5] {
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
		if c.Name == "git" && !c.Optional {
			t.Errorf("%s: Optional = false, want true", c.Name)
		}
		if c.Name != "git" && c.Optional {
			t.Errorf("%s: Optional = true, want false", c.Name)
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

// An unknown distro still reports the problem and still names the packages
// to install. It invents no command to run. The test iterates binChecks
// directly, not whatever RunChecks(DistroUnknown) returns, so a nil Fix
// fails the test instead of being silently skipped.
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

func TestRunChecksReportsGitAsOptionalWithDistroFix(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	checks := RunChecks(DistroArch)
	for _, c := range checks {
		if c.Name != "git" {
			continue
		}
		if !c.Optional {
			t.Fatal("git is required by recipe commands but must be optional for host readiness")
		}
		if c.OK {
			t.Fatal("git unexpectedly found with an empty PATH")
		}
		if got := strings.Join(c.Fix, " "); !strings.Contains(got, "git") || !strings.Contains(got, "pacman") {
			t.Fatalf("git fix = %v, want an actionable Arch install command", c.Fix)
		}
		return
	}
	t.Fatal("RunChecks omitted the optional git check")
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
