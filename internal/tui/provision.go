package tui

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

var errNotRunning = errors.New("not running — start it first")

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
		m.status = v.Name + ": cloud VMs provision at first boot via cloud-init — recipes are applied automatically; recreate the VM to change them"
		return nil
	}
	if len(v.Recipes) == 0 {
		m.status = v.Name + ": no recipes selected — nothing to provision"
		return nil
	}
	if m.provisioning[v.Name] {
		return nil
	}
	m.provisioning[v.Name] = true
	m.status = "provisioning " + v.Name + "…"
	return provision(v)
}
