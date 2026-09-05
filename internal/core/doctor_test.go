package core

import (
	"strings"
	"testing"
)

// TestDoctorStructure asserts on the shape Doctor promises, not on what
// happens to be installed on the machine running the test: an empty PATH
// makes every LookPath-based check deterministically fail, regardless of
// whether the real qemu-system-x86_64/qemu-img/ssh/xorriso are present here.
// /dev/kvm is the one probe this can't force either way (it isn't reached
// through PATH), so it is only checked for the OK<->Fix invariant below, not
// for a specific pass/fail outcome.
func TestDoctorStructure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	checks := Doctor()

	if len(checks) == 0 {
		t.Fatal("Doctor() returned no checks")
	}

	seen := map[string]bool{}
	for _, c := range checks {
		if c.Name == "" {
			t.Errorf("check with empty Name: %+v", c)
		}
		seen[c.Name] = true

		if c.OK {
			// A passing check has nothing to complain about and nothing to
			// fix; a Fix on a passing check would be a caller-visible lie
			// ("here's how to install a thing you already have").
			if len(c.Fix) != 0 {
				t.Errorf("%s: OK but carries a Fix %v", c.Name, c.Fix)
			}
		} else {
			// A failing check must explain itself: Detail is the human-
			// readable "what is wrong" the design doc asks for.
			if c.Detail == "" {
				t.Errorf("%s: failing check has no Detail", c.Name)
			}
		}
	}

	// The union of both prior doctors' checks must be present: the four
	// installer binChecks plus /dev/kvm. Losing one silently is the failure to
	// catch.
	want := []string{"qemu-system-x86_64", "qemu-img", "ssh", "xorriso", "/dev/kvm"}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("Doctor() is missing the %q check", name)
		}
	}

	// With PATH cleared, every LookPath-backed check must fail and must
	// offer a Fix; this part IS safe to pin down, because clearing PATH
	// makes the outcome deterministic rather than dependent on the host.
	for _, c := range checks {
		if c.Name == "/dev/kvm" {
			continue
		}
		if c.OK {
			t.Errorf("%s: OK with an empty PATH", c.Name)
		}
		if len(c.Fix) == 0 {
			t.Errorf("%s: a failed check must carry a Fix", c.Name)
		}
	}
}

// TestDoctorNoSSHKeygenCheck pins that ssh-keygen gets no separate check: it
// ships in the same package as ssh on every supported distro.
func TestDoctorNoSSHKeygenCheck(t *testing.T) {
	for _, c := range Doctor() {
		if strings.Contains(c.Name, "ssh-keygen") {
			t.Errorf("Doctor() has a separate ssh-keygen check: %+v", c)
		}
	}
}

func TestDoctorCarriesOptionalGitFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, c := range Doctor() {
		if c.Name != "git" {
			continue
		}
		if !c.Optional {
			t.Fatal("Doctor() lost git's optional marker")
		}
		if len(c.Fix) == 0 {
			t.Fatal("optional git failure has no install guidance")
		}
		return
	}
	t.Fatal("Doctor() has no git check")
}
