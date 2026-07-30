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
