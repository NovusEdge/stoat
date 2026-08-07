package tui

import (
	"context"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// After a VM with recipes starts, stoat watches for sshd. It then offers to
// provision the VM. It does not provision without asking.
//
// An unasked shell script inside a guest looks like a bug the first time it
// runs. A recipe can take minutes and install hundreds of packages.

// sshReadyMsg says a VM that was just started is now accepting ssh.
type sshReadyMsg struct{ name string }

// awaitSSH waits for sshd on a freshly started VM.
//
// A failure stays silent. The user did not ask for this watch. A VM that
// never becomes reachable (a disk VM with no OS, a guest that fails to
// boot) leaves the UI unchanged instead of raising an unrequested error.
// Pressing "p" still reports the real status.
//
// ctx carries the same WaitTimeout ceiling sshx.Provision waits under, so a
// VM that never comes up gives up on the same schedule.
func awaitSSH(v core.VM) tea.Cmd {
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
// The answer differs by mode because the filesystem differs by mode:
//
//   - live: the root is a tmpfs overlay. A previous run is gone after
//     reboot, so stoat offers every time.
//   - disk/cloud: packages persist. Stoat offers again only after a failed
//     run, not a successful one.
func wantsAutoProvisionPrompt(v core.VM) bool {
	if len(v.Recipes) == 0 {
		return false
	}
	// cloud-init applies a cloud VM's recipes at first boot; there is nothing
	// for ssh provisioning to do, and startProvision refuses it anyway.
	if v.Mode == "cloud" {
		return false
	}
	// An uninstalled disk VM runs its own installer on a tmpfs root that the
	// install later replaces. Its sshd may already answer, so this check
	// cannot rely on reachability alone.
	// Once installed, stoat offers only when the last run did not succeed.
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
func lastProvisionSucceeded(v core.VM) bool {
	b := tailBytes(v.Paths.ApplyLog, provTailBytes)
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
func autoProvisionPrompt(v core.VM) string {
	names := make([]string, len(v.Recipes))
	for i, r := range v.Recipes {
		names[i] = recipeLabel(r)
	}
	return v.Name + " is up, run " + strings.Join(names, ", ") + " now? y/N"
}

// ensureNoStaleLog removes a provision log left by a previous boot of a live
// VM.
//
// The log lives on the host and survives the reboot; nothing else does.
// Without removing it, lastProvisionSucceeded and the detail pane's tail
// both describe a run whose effects the reboot already wiped.
func ensureNoStaleLog(v core.VM) {
	if v.Mode != "live" {
		return
	}
	_ = os.Remove(v.Paths.ApplyLog)
}
