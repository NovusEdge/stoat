package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
)

var errNotRunning = errors.New("not running: start it first")

// installerName is what the user types at the guest console. Only Alpine has
// setup-alpine. A BYO Fedora, Debian, or unknown ISO has its own installer,
// so naming the wrong one is worse than staying general.
//
// installerName takes the OS string, not a VM. Its two callers hold
// different VM types: the list holds core.VM, the detail screen holds
// config.VM. Only one field is ever read.
func installerName(osName string) string {
	if os, ok := guest.Lookup(osName); ok && os.Installer != "" {
		return os.Installer
	}
	return "the installer"
}

type provisionDoneMsg struct {
	name string
	err  error
}

// provision runs a VM's recipes over ssh via core.Apply. Output still goes to
// last-provision.log (core.Apply calls sshx.Provision unchanged), which the
// detail view tails, so nothing is streamed through the model.
func provision(v core.VM) tea.Cmd {
	return func() tea.Msg {
		// The state the user was looking at when they pressed the key. If it
		// has gone stale since, core.Apply refuses with its own ErrNotRunning
		// rather than acting on the stale answer.
		if v.State != core.StateRunning {
			return provisionDoneMsg{v.Name, errNotRunning}
		}
		// No cancellation source reaches here: the TUI has no "abort
		// provision" key.
		//
		// core.Apply refuses a cloudinit-backed VM with ErrAppliedAtBoot.
		// startProvision's refusal below should catch that case first, but if
		// it reaches here anyway, app.go's generic err.Error() toast still
		// reports it.
		return provisionDoneMsg{v.Name, core.Apply(context.Background(), v.Name, core.ApplyOpts{})}
	}
}

// startProvision is the shared "p" handler for both the list and detail
// screens. It guards against a provision already in flight for v, marks it
// as running, sets the status line, and returns the command to run it.
//
// A VM with zero recipes has nothing for sshx.Provision to do. It used to
// loop zero times, write "done", and report "provisioned" anyway, a false
// success message. This case is short-circuited before m.provisioning is
// touched, so it does not block a second real provision from starting right
// after.
func (m *model) startProvision(v core.VM) tea.Cmd {
	if backend.For(cfgVM(v)).Name() == "cloudinit" {
		// Keyed on the BACKEND, not v.Mode == "cloud". The edit screen's mode
		// switch can produce mode="disk" with backend="cloudinit" (D9a), and
		// this refusal must still catch that state.
		//
		// cloud-init's packages: list is baked into the seed and runs only at
		// first boot. ssh-based provisioning has nothing to do there, and a
		// cloud recipe is #cloud-config YAML, not a shell script, so piping
		// it into `sh -s` fails.
		//
		// core.Apply refuses the same state with ErrAppliedAtBoot. This check
		// shows the user the refusal before anything starts, instead of
		// after a failed attempt.
		return m.showToast(v.Name+": cloud VMs provision at first boot via cloud-init. Recipes are applied automatically; recreate the VM to change them", true)
	}
	// A disk VM still boots its installer ISO until its OS is on disk. sshd
	// there belongs to the installer, not the system being built, so
	// provisioning it would run recipes against a tmpfs about to be thrown
	// away. Without this check, the user waits the full 90s ssh timeout and
	// gets "ssh not reachable", which says nothing about the real cause.
	if v.Mode == "disk" && !v.Installed {
		if v.OS == "alpine" {
			return m.showToast(v.Name+": installing itself; wait for it to finish and reboot, "+
				"then stoat notices the install and offers to provision", true)
		}
		return m.showToast(v.Name+": not installed yet, run "+installerName(v.OS)+
			" at the console, then stop and start it", true)
	}
	if len(v.Recipes) == 0 {
		return m.showToast(v.Name+": no recipes selected, nothing to provision", true)
	}
	if _, running := m.provisioning[v.Name]; running {
		return nil
	}
	// The spinner and elapsed clock start here, not when the first log line
	// lands, so the UI accounts for the ssh wait too, which on a cold live
	// boot is the longest part of the whole run.
	m.provisioning[v.Name] = provState{start: time.Now(), step: "starting"}
	m.status = ""
	return tea.Batch(provision(v), m.spin.Tick)
}
