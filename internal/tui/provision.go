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

// installerName is what the user actually types at the guest console. Only
// Alpine has setup-alpine; a BYO Fedora/Debian/unknown ISO has its own, so
// naming the wrong one is worse than staying general.
//
// Takes the OS string rather than a VM: its two kinds of caller now hold
// different VM types (the list a core.VM, the detail screen a config.VM), and
// one field is all it ever read.
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
		// No cancellation source reaches here yet: the TUI has no "abort
		// provision" key, so this is a call site noted for the caller to
		// decide whether one should exist, not a design decision made here.
		// core.Apply refuses a cloudinit-backed VM with ErrAppliedAtBoot on
		// its own; that case should already be caught by startProvision's
		// refusal below, but app.go's generic err.Error() toast reports it
		// honestly if it ever reaches here anyway.
		return provisionDoneMsg{v.Name, core.Apply(context.Background(), v.Name, core.ApplyOpts{})}
	}
}

// startProvision is the shared "p" handler for both the list and detail
// screens: guard against a provision already in flight for v, mark it as
// running, set the status line, and return the command to run it.
//
// A VM with zero recipes never had anything for sshx.Provision to do, but it
// used to loop zero times, write "done", and report "provisioned" anyway,
// a success message for having done nothing. That case is short-circuited
// here, before m.provisioning is even touched, so it neither lies about
// what happened nor blocks a second real provision from starting right
// after.
func (m *model) startProvision(v core.VM) tea.Cmd {
	if backend.For(cfgVM(v)).Name() == "cloudinit" {
		// Keyed on the BACKEND, not v.Mode == "cloud": the edit screen's mode
		// switch can produce mode="disk" with backend="cloudinit" (D9a), a
		// state this refusal must still catch. cloud-init's packages: list is
		// baked into the seed and only runs at first boot; there is nothing
		// for ssh-based provisioning to do, and a cloud recipe is
		// #cloud-config YAML, not a shell script, so piping it into `sh -s`
		// would just fail. core.Apply refuses the same state with
		// ErrAppliedAtBoot; this refusal exists so the user sees it before
		// anything starts, rather than after a failed attempt.
		return m.showToast(v.Name+": cloud VMs provision at first boot via cloud-init. Recipes are applied automatically; recreate the VM to change them", true)
	}
	// A disk VM is still booting its installer ISO until its OS is on disk;
	// sshd there belongs to the installer, not to the system being built, so
	// provisioning it would run recipes against a tmpfs about to be thrown
	// away. Without this the user waits the full 90s ssh timeout and gets
	// "ssh not reachable", which says nothing about the real cause.
	if v.Mode == "disk" && !v.Installed {
		return m.showToast(v.Name+": not installed yet, run "+installerName(v.OS)+
			" in the qemu window, then stop and start it (stoat notices the install itself)", true)
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
