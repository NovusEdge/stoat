package tui

import (
	"context"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// After starting a VM that has recipes, stoat watches for sshd to come up and
// then OFFERS to provision; it does not just do it. Running a shell script
// inside a guest without being asked is the kind of helpfulness that is
// indistinguishable from a bug the first time it surprises someone, and a
// recipe can take minutes and pull hundreds of packages.

// sshReadyMsg says a VM that was just started is now accepting ssh.
type sshReadyMsg struct{ name string }

// awaitSSH waits for sshd on a freshly started VM. A failure is silent on
// purpose: the user did not ask for this, so a VM that never becomes
// reachable (a disk VM with no OS, a guest that fails to boot) should leave
// the UI exactly as it was rather than raising an error about a thing nobody
// requested. Pressing "p" still reports properly.
//
// core.Wait(UntilReachable) replaces the old direct sshx.Wait call; the ctx
// carries the same WaitTimeout ceiling sshx.Provision itself waits under, so
// a VM that never comes up gives up on the same schedule either path takes.
func awaitSSH(v *config.VM) tea.Cmd {
	name := v.Name
	return func() tea.Msg {
		// No cancellation source reaches here: this is a background watch
		// started right after the VM boots, not tied to any in-flight
		// caller request.
		ctx, cancel := context.WithTimeout(context.Background(), sshx.WaitTimeout)
		defer cancel()
		if err := core.Wait(ctx, name, core.UntilReachable); err != nil {
			return nil
		}
		return sshReadyMsg{name}
	}
}

// wantsAutoProvisionPrompt reports whether stoat should offer to provision v
// once it is reachable.
//
// The "already provisioned" question is answered differently per mode, and
// the difference is not a preference: it is what the filesystem does:
//
//   - live: the root is a tmpfs overlay, so a previous run is GONE after the
//     reboot. Offering every time is correct, not nagging.
//   - disk/cloud: packages persist, so offering again after a successful run
//     would be asking the user to redo work that is still there.
func wantsAutoProvisionPrompt(v *config.VM) bool {
	if len(v.Recipes) == 0 {
		return false
	}
	// cloud-init applies a cloud VM's recipes at first boot; there is nothing
	// for ssh provisioning to do, and startProvision refuses it anyway.
	if v.Mode == "cloud" {
		return false
	}
	// An uninstalled disk VM is running its installer, whose root is a tmpfs
	// that the install replaces, and sshd there may well answer, which is
	// exactly why this has to be checked rather than left to reachability.
	// Once installed, only offer when the last run didn't already succeed.
	if v.Mode == "disk" && !v.Installed {
		return false
	}
	if v.Mode != "live" && lastProvisionSucceeded(v) {
		return false
	}
	return true
}

// lastProvisionSucceeded reports whether the VM's most recent provision run
// finished cleanly. sshx.Provision writes "done" as the final line, and
// truncates the file at the start of every run, so the tail is unambiguous.
func lastProvisionSucceeded(v *config.VM) bool {
	b := tailBytes(v.ProvisionLogPath(), provTailBytes)
	if len(b) == 0 {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l == "done"
		}
	}
	return false
}

// autoProvisionPrompt is the y/N line shown when a started VM becomes
// reachable. It names the recipes so the answer is informed: "provision
// work?" says nothing about what is about to run.
func autoProvisionPrompt(v *config.VM) string {
	names := make([]string, len(v.Recipes))
	for i, r := range v.Recipes {
		names[i] = recipeLabel(r)
	}
	return v.Name + " is up, run " + strings.Join(names, ", ") + " now? y/N"
}

// ensureNoStaleLog removes a provision log left by a previous boot of a LIVE
// VM. Without it, lastProvisionSucceeded and the detail pane's tail both
// describe a run whose effects were wiped by the reboot. The log is the only
// thing that survived, because it lives on the host.
func ensureNoStaleLog(v *config.VM) {
	if v.Mode != "live" {
		return
	}
	_ = os.Remove(v.ProvisionLogPath())
}
