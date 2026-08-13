package tui

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// After a VM starts, stoat watches for sshd and provisions it once it is
// reachable, with no keypress.

// sshReadyMsg says a VM that was just started is now accepting ssh.
type sshReadyMsg struct{ name string }

// installRestartedMsg says an uninstalled disk VM's installer powered off
// and stoat restarted it to boot the disk. A nil-error timeout produces no
// message at all, mirroring awaitSSH.
type installRestartedMsg struct{ name string }

// awaitInstall waits for an uninstalled disk VM's installer to power off,
// then restarts the VM, the same sequence internal/cli/run_vm.go's runUp
// runs synchronously. The TUI runs it as a background watch instead, since
// blocking the whole program for up to core.InstallTimeout would freeze
// every other VM's controls. AutoRestartAfterInstall applies InstallTimeout
// itself, so no ctx here needs its own deadline.
//
// A failure stays silent, matching awaitSSH: the user did not ask for this
// watch, and a failed or still-running installer is visible in the list
// already.
func awaitInstall(v core.VM) tea.Cmd {
	name := v.Name
	return func() tea.Msg {
		restarted, err := core.AutoRestartAfterInstall(context.Background(), name)
		if err != nil || !restarted {
			return nil
		}
		return installRestartedMsg{name}
	}
}

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

// needsAutoProvision reports whether stoat should provision v once it is
// reachable.
//
//   - An uninstalled disk VM's sshd belongs to its own installer, running on
//     a tmpfs root the install later replaces; reachability alone cannot
//     tell that apart from the real system, so it is excluded up front.
//   - A live VM's root is a tmpfs overlay: every reboot erases whatever a
//     previous run installed, so core.NeedsProvision's Applied bookkeeping
//     (which persists on the host, across reboots) cannot answer for it. Any
//     recipe at all means there is work to redo.
//   - Everything else (including cloud VMs) defers to core.NeedsProvision.
//     Cloud-init runs the seed's recipes at first boot; core.Apply discovers
//     those via marker files and only runs new or changed recipes.
func needsAutoProvision(v core.VM) bool {
	if v.Mode == "disk" && !v.Installed {
		return false
	}
	if v.Mode == "live" {
		return len(v.Recipes) > 0
	}
	needs, err := core.NeedsProvision(toConfigVM(v))
	return err == nil && needs
}

// toConfigVM carries the fields core.NeedsProvision reads: the OS and
// backend the recipe run mode logic checks, and the Applied record it
// compares script hashes against. It is a narrower relative of app.go's
// cfgVM, built for this one call instead of sshx's identity fields.
func toConfigVM(v core.VM) *config.VM {
	var applied map[string]config.AppliedRecipe
	if len(v.Applied) > 0 {
		applied = make(map[string]config.AppliedRecipe, len(v.Applied))
		for name, a := range v.Applied {
			applied[name] = config.AppliedRecipe{Version: a.Version, Hash: a.Hash, At: a.At}
		}
	}
	return &config.VM{
		OS: v.OS, Mode: v.Mode, Share: v.Share, Recipes: v.Recipes, Applied: applied,
	}
}

// ensureNoStaleLog removes a provision log left by a previous boot of a live
// VM.
//
// The log lives on the host and survives the reboot; nothing else does.
// Without removing it, the detail pane's tail describes a run whose effects
// the reboot already wiped.
func ensureNoStaleLog(v core.VM) {
	if v.Mode != "live" {
		return
	}
	_ = os.Remove(v.Paths.ApplyLog)
}
