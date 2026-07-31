package tui

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

var errNotRunning = errors.New("not running — start it first")

// installerName is what the user actually types at the guest console. Only
// Alpine has setup-alpine; a BYO Fedora/Debian/unknown ISO has its own, so
// naming the wrong one is worse than staying general.
func installerName(v *config.VM) string {
	if v.OS == "alpine" {
		return "setup-alpine"
	}
	return "the installer"
}

type provisionDoneMsg struct {
	name string
	err  error
}

// provision runs a VM's recipes over ssh. Output goes to last-provision.log,
// which the detail view tails, so nothing is streamed through the model.
func provision(v *config.VM) tea.Cmd {
	return func() tea.Msg {
		if !qemu.Running(v) {
			return provisionDoneMsg{v.Name, errNotRunning}
		}
		return provisionDoneMsg{v.Name, sshx.Provision(v)}
	}
}

// startProvision is the shared "p" handler for both the list and detail
// screens: guard against a provision already in flight for v, mark it as
// running, set the status line, and return the command to run it.
//
// A VM with zero recipes never had anything for sshx.Provision to do, but it
// used to loop zero times, write "done", and report "provisioned" anyway —
// a success message for having done nothing. That case is short-circuited
// here, before m.provisioning is even touched, so it neither lies about
// what happened nor blocks a second real provision from starting right
// after.
func (m *model) startProvision(v *config.VM) tea.Cmd {
	if v.Mode == "cloud" {
		// cloud-init's packages: list is baked into the seed and only runs
		// at first boot; there is nothing for ssh-based provisioning to do,
		// and a cloud recipe is #cloud-config YAML, not a shell script, so
		// piping it into `sh -s` would just fail.
		return m.showToast(v.Name+": cloud VMs provision at first boot via cloud-init — recipes are applied automatically; recreate the VM to change them", true)
	}
	// A disk VM boots its installer ISO with no key and no sshd: apkovl.Build
	// runs for live mode only (qemu/run.go), so there is nothing for ssh to
	// connect to until the guest OS is actually installed. Without this the
	// user waits the full 90s ssh timeout and gets "ssh not reachable", which
	// says nothing about the real cause.
	if v.Mode == "disk" && !v.Installed {
		return m.showToast(v.Name+": not installed yet — run "+installerName(v)+
			" in the qemu window, then press i (marks it installed) before provisioning", true)
	}
	if len(v.Recipes) == 0 {
		return m.showToast(v.Name+": no recipes selected — nothing to provision", true)
	}
	if _, running := m.provisioning[v.Name]; running {
		return nil
	}
	// The spinner and elapsed clock start here, not when the first log line
	// lands, so the UI accounts for the ssh wait too — which on a cold live
	// boot is the longest part of the whole run.
	m.provisioning[v.Name] = provState{start: time.Now(), step: "starting"}
	m.status = ""
	return tea.Batch(provision(v), m.spin.Tick)
}
