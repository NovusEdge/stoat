package tui

import (
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func autoVM(t *testing.T, mode string, recipes []string) core.VM {
	t.Helper()
	dir := t.TempDir()
	return core.VM{
		Name: "vm", Mode: mode, OS: "alpine", Installed: true,
		SSHPort: 2200, Recipes: recipes,
		State: core.StateRunning,
		Paths: core.Paths{Dir: dir, ApplyLog: filepath.Join(dir, "last-provision.log")},
	}
}

// TestNeedsAutoProvision covers who gets provisioned automatically and who
// doesn't. The live-vs-disk difference is not a preference: a live VM's root
// is a tmpfs overlay, so a previous run is genuinely gone after the reboot,
// while a disk VM's packages persist and its Applied record can be trusted.
func TestNeedsAutoProvision(t *testing.T) {
	recipes := []string{"xfce.alpine.sh"}

	cases := []struct {
		name string
		vm   core.VM
		want bool
	}{
		{"live, never provisioned", autoVM(t, "live", recipes), true},
		{"live, no recipes", autoVM(t, "live", nil), false},
		{"disk, never provisioned", autoVM(t, "disk", recipes), true},
		{"disk, no recipes, no share", autoVM(t, "disk", nil), false},
		{"cloud, cloud-init already did it", autoVM(t, "cloud", recipes), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsAutoProvision(c.vm); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	// A disk VM with no OS installed never becomes reachable as itself; its
	// sshd, if any, belongs to the installer.
	uninstalled := autoVM(t, "disk", recipes)
	uninstalled.Installed = false
	if needsAutoProvision(uninstalled) {
		t.Error("wanted to auto-provision a disk VM with no OS installed")
	}

	// A live VM's tmpfs root wipes every reboot. Its Applied record, saved on
	// the host, survives, but it describes a filesystem that is gone; a live
	// VM auto-applies every boot regardless of what Applied says.
	live := autoVM(t, "live", recipes)
	live.Applied = map[string]core.AppliedRecipe{"xfce.alpine.sh": {Version: "1.0", Hash: "whatever"}}
	if !needsAutoProvision(live) {
		t.Error("a live VM must auto-provision every boot even with a stale Applied record")
	}

	// A disk VM with a share still needs the idempotent share-mount step,
	// even once every recipe is applied.
	shared := autoVM(t, "disk", nil)
	shared.Share = "/host/path"
	if !needsAutoProvision(shared) {
		t.Error("a disk VM with a share must still auto-provision for the mount step")
	}
}

// TestSSHReadyAutoProvisions is the sshReadyMsg handler's main path: it
// starts a provision run itself, with no confirmation state in between.
func TestSSHReadyAutoProvisions(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh"})
	m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
		provisioning: map[string]provState{}, vms: []core.VM{v}}

	out, cmd := m.Update(sshReadyMsg{name: v.Name})
	after := out.(model)

	if _, running := after.provisioning[v.Name]; !running {
		t.Error("sshReadyMsg did not start a provision run")
	}
	if cmd == nil {
		t.Error("sshReadyMsg returned no command")
	}
	if after.status != "" {
		t.Errorf("status = %q, want no prompt", after.status)
	}
}

// TestSSHReadyDoesNothingWhenNothingToProvision: a VM with no recipes and no
// share must not start a run, and must show nothing.
func TestSSHReadyDoesNothingWhenNothingToProvision(t *testing.T) {
	v := autoVM(t, "disk", nil)
	m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
		provisioning: map[string]provState{}, vms: []core.VM{v}}

	out, cmd := m.Update(sshReadyMsg{name: v.Name})
	after := out.(model)

	if len(after.provisioning) != 0 {
		t.Error("started a provision run with nothing to provision")
	}
	if cmd != nil {
		t.Error("returned a command with nothing to provision")
	}
	if after.status != "" {
		t.Errorf("status = %q, want nothing shown", after.status)
	}
}

// TestSSHReadyNeverPreemptsADeletePrompt: an offer arriving on a 90-second
// timer must not clear the status line out from under a pending delete
// confirmation, a more consequential question the user is mid-answer.
func TestSSHReadyNeverPreemptsADeletePrompt(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh"})
	m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
		provisioning: map[string]provState{}, vms: []core.VM{v},
		pendingDelete: &v, status: "delete vm? y/N"}

	out, _ := m.Update(sshReadyMsg{name: v.Name})
	after := out.(model)

	if len(after.provisioning) != 0 {
		t.Error("auto-provision ran ahead of a pending delete confirmation")
	}
	if after.status != "delete vm? y/N" {
		t.Errorf("status = %q, want the delete prompt intact", after.status)
	}
}
