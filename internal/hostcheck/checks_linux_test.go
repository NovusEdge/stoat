//go:build linux

package hostcheck

import (
	"strings"
	"testing"
)

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
