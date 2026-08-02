package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestSSHIntoArgsUsesConfiguredSSHUser: the live bug. A cloud VM's seeded
// account is v.SSHUser (e.g. "stoat") and root is locked (see
// internal/cloudinit/cloudinit.go's user block), so the interactive ssh
// path must target it, not a hardcoded root@127.0.0.1.
func TestSSHIntoArgsUsesConfiguredSSHUser(t *testing.T) {
	v := &config.VM{Name: "x", SSHPort: 2201, SSHUser: "stoat"}
	got := strings.Join(sshIntoArgs(v), " ")

	if !strings.Contains(got, "stoat@127.0.0.1") {
		t.Errorf("expected stoat@127.0.0.1 in argv, got: %s", got)
	}
	if strings.Contains(got, "root@127.0.0.1") {
		t.Errorf("must not target root@127.0.0.1 when SSHUser is set, got: %s", got)
	}
}

// TestSSHIntoArgsFallsBackToRoot: no SSHUser recorded (apkovl/ssh-backend
// VMs, both unlocked-root) must still target root.
func TestSSHIntoArgsFallsBackToRoot(t *testing.T) {
	v := &config.VM{Name: "x", SSHPort: 2201}
	got := strings.Join(sshIntoArgs(v), " ")

	if !strings.Contains(got, "root@127.0.0.1") {
		t.Errorf("expected root@127.0.0.1 fallback, got: %s", got)
	}
}

// TestUnreachableMsgNamesRealInstaller: the failure text used to hardcode
// "setup-alpine" regardless of guest OS. A non-Alpine VM must not see it.
func TestUnreachableMsgNamesRealInstaller(t *testing.T) {
	v := &config.VM{Name: "x", SSHPort: 2201, OS: "fedora"}
	got := unreachableMsg(v)

	if strings.Contains(got, "setup-alpine") {
		t.Errorf("must not name setup-alpine for a non-alpine VM, got: %q", got)
	}
	if !strings.Contains(got, "the installer") {
		t.Errorf("expected the generic installer name for fedora, got: %q", got)
	}
}

// TestUnreachableMsgNamesAlpineInstaller: an Alpine VM keeps the specific,
// correct name.
func TestUnreachableMsgNamesAlpineInstaller(t *testing.T) {
	v := &config.VM{Name: "x", SSHPort: 2201, OS: "alpine"}
	got := unreachableMsg(v)

	if !strings.Contains(got, "setup-alpine") {
		t.Errorf("expected setup-alpine named for an alpine VM, got: %q", got)
	}
}
