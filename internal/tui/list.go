package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

func (m model) current() *config.VM {
	if m.cursor < 0 || m.cursor >= len(m.vms) {
		return nil
	}
	return m.vms[m.cursor]
}

func startVM(v *config.VM) tea.Cmd {
	return func() tea.Msg {
		if err := qemu.Start(v); err != nil {
			return statusMsg(err.Error())
		}
		return statusMsg(v.Name + " started")
	}
}

func stopVM(v *config.VM) tea.Cmd {
	return func() tea.Msg {
		if err := qemu.Stop(v); err != nil {
			return statusMsg(err.Error())
		}
		return statusMsg(v.Name + " stopped")
	}
}

func (m model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// The delete confirmation prompt owns all keys while pending: "y"
	// confirms, anything else cancels. This must run before the normal
	// switch below because "n" is otherwise bound to "new VM".
	if m.pendingDelete != nil {
		if key.String() == "y" {
			v := m.pendingDelete
			m.pendingDelete = nil
			m.status = ""
			return m, tea.Sequence(deleteVM(v), loadVMs)
		}
		m.pendingDelete = nil
		m.status = ""
		return m, nil
	}

	v := m.current()

	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.vms)-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if v == nil {
			break
		}
		m.status = ""
		if qemu.Running(v) {
			return m, tea.Sequence(stopVM(v), loadVMs)
		}
		return m, tea.Sequence(startVM(v), loadVMs)
	case "n":
		m.form = newForm()
		m.screen = screenForm
	case "right", "l":
		if v != nil {
			m.detail = newDetail(v)
			m.screen = screenDetail
			m.detailGen++
			return m, tick(m.detailGen)
		}
	case "s":
		if v != nil && qemu.Running(v) {
			return m, sshInto(v)
		}
		m.status = "not running"
	case "d":
		if v != nil {
			if qemu.Running(v) {
				m.status = "stop " + v.Name + " first"
				break
			}
			m.status = "delete " + v.Name + "? y/N"
			m.screen = screenList
			m.pendingDelete = v
		}
	case "esc":
		m.pendingDelete = nil
		m.status = ""
	}
	return m, nil
}

func deleteVM(v *config.VM) tea.Cmd {
	return func() tea.Msg {
		name := v.Name
		if err := v.Delete(); err != nil {
			return statusMsg(err.Error())
		}
		return statusMsg(name + " deleted")
	}
}

func (m model) viewList() string {
	var b strings.Builder
	b.WriteString(banner() + "\n\n")

	if m.preflight != "" {
		b.WriteString(errStyle.Render("  "+m.preflight) + "\n\n")
	}

	if len(m.vms) == 0 {
		b.WriteString(dimStyle.Render("  no vms yet — press n to create one") + "\n")
	}

	for i, v := range m.vms {
		running := qemu.Running(v)
		dot, dotStyle := "○", downStyle
		state := dimStyle.Render("—")
		if running {
			dot, dotStyle = "●", upStyle
			state = fmt.Sprintf("up %s  :%d", qemu.Uptime(v), v.SSHPort)
		}
		row := fmt.Sprintf("%s %-14s %-5s %5dM %2dc  %s",
			dotStyle.Render(dot), v.Name, v.Mode, v.RAM, v.CPUs, state)
		cursor := "  "
		if i == m.cursor {
			cursor = selStyle.Render("❯ ")
			row = selStyle.Render(row)
		}
		b.WriteString(cursor + row + "\n")
	}

	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(warnStyle.Render("  "+m.status) + "\n")
	}
	b.WriteString(dimStyle.Render(
		"  ↵ start/stop   → details   s ssh   n new   d delete   q quit") + "\n")
	return b.String()
}
