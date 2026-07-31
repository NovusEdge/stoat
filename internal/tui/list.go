package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// The list shows good VMs (m.vms) followed by broken ones (m.broken); the
// cursor ranges over both as one sequence. current and currentBroken split
// it back apart: exactly one of them returns non-nil for any valid cursor
// position.
func (m model) current() *config.VM {
	if m.cursor < 0 || m.cursor >= len(m.vms) {
		return nil
	}
	return m.vms[m.cursor]
}

func (m model) currentBroken() *config.Broken {
	i := m.cursor - len(m.vms)
	if i < 0 || i >= len(m.broken) {
		return nil
	}
	return &m.broken[i]
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
	if m.pendingDelete != nil || m.pendingDeleteBroken != "" {
		if key.String() == "y" {
			if m.pendingDelete != nil {
				v := m.pendingDelete
				m.pendingDelete = nil
				m.status = ""
				return m, tea.Sequence(deleteVM(v), loadVMs)
			}
			name := m.pendingDeleteBroken
			m.pendingDeleteBroken = ""
			m.status = ""
			return m, tea.Sequence(deleteBrokenVM(name), loadVMs)
		}
		m.pendingDelete = nil
		m.pendingDeleteBroken = ""
		m.status = ""
		return m, nil
	}

	v := m.current()
	broken := m.currentBroken()
	total := len(m.vms) + len(m.broken)

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "j", "down":
		if m.cursor < total-1 {
			m.cursor++
		}
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
	case "enter":
		if v == nil {
			if broken != nil {
				m.status = broken.Name + ": broken vm.toml — cannot start (d to delete)"
			}
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
		m.showHelp = false
	case "right", "l":
		if v != nil {
			m.detail = newDetail(v)
			m.screen = screenDetail
			m.detailGen++
			m.showHelp = false
			return m, tick(m.detailGen)
		}
		if broken != nil {
			m.status = broken.Name + ": broken vm.toml — cannot start (d to delete)"
		}
	case "s":
		if v != nil && qemu.Running(v) {
			return m, sshInto(v)
		}
		m.status = "not running"
	case "p":
		if v != nil {
			return m, m.startProvision(v)
		}
	case "d":
		if v != nil {
			if qemu.Running(v) {
				m.status = "stop " + v.Name + " first"
				break
			}
			m.status = "delete " + v.Name + "? y/N"
			m.screen = screenList
			m.pendingDelete = v
		} else if broken != nil {
			m.status = "delete " + broken.Name + "? y/N"
			m.screen = screenList
			m.pendingDeleteBroken = broken.Name
		}
	case "esc":
		m.pendingDelete = nil
		m.pendingDeleteBroken = ""
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

// deleteBrokenVM removes a broken VM's directory by name. There is no
// *config.VM to call Delete on — the whole point is that its vm.toml
// couldn't be parsed into one — so this reimplements Delete's data-root
// containment check directly against the directory path.
func deleteBrokenVM(name string) tea.Cmd {
	return func() tea.Msg {
		dir := filepath.Join(config.Root(), name)
		if filepath.Dir(dir) != config.Root() {
			return statusMsg(fmt.Sprintf("refusing to delete %q: outside the data root", dir))
		}
		if err := os.RemoveAll(dir); err != nil {
			return statusMsg(err.Error())
		}
		return statusMsg(name + " deleted")
	}
}

// brokenReason returns a short, single-line reason string for a broken
// vm.toml's parse error, suitable for a list row.
func brokenReason(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 60
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}

// viewList renders the list screen's body — everything below the banner,
// which View composes separately so it can be centered over this block
// instead of sharing its left edge (see View's doc comment). The VM rows
// live inside a pane; the status line and footer sit outside it, left-
// aligned to the pane's own left edge.
func (m model) viewList() string {
	var rows strings.Builder

	if m.preflight != "" {
		rows.WriteString(errStyle.Render(m.preflight) + "\n\n")
	}

	if len(m.vms) == 0 && len(m.broken) == 0 {
		rows.WriteString(dimStyle.Render("no vms yet — press n to create one"))
	}

	for i, v := range m.vms {
		running := qemu.Running(v)
		dot, dotStyle := "○", downStyle
		state := dimStyle.Render("—")
		if running {
			dot, dotStyle = "●", upStyle
			state = fmt.Sprintf("up %s  :%d", qemu.Uptime(v), v.SSHPort)
		}
		// The dot is rendered OUTSIDE the selection wrap on purpose. A styled
		// substring ends in \x1b[0m, which resets the enclosing style too, so
		// wrapping a row that starts with a coloured dot left everything after
		// the dot unhighlighted — the selected row was marked by the ❯ alone.
		label := fmt.Sprintf("%-14s %-5s %5dM %2dc  %s", v.Name, v.Mode, v.RAM, v.CPUs, state)
		cursor := "  "
		if i == m.cursor {
			cursor = selStyle.Render("❯ ")
			label = selStyle.Render(label)
		}
		row := dotStyle.Render(dot) + " " + label
		if i > 0 {
			rows.WriteString(rowGap)
		}
		rows.WriteString(cursor + row)
	}

	for i, bv := range m.broken {
		plain := fmt.Sprintf("✗ %-14s broken: %s", bv.Name, brokenReason(bv.Err))
		cursor := "  "
		row := downStyle.Render(plain)
		idx := len(m.vms) + i
		if idx == m.cursor {
			cursor = selStyle.Render("❯ ")
			row = selStyle.Render(plain)
		}
		if i > 0 || len(m.vms) > 0 {
			rows.WriteString(rowGap)
		}
		rows.WriteString(cursor + row)
	}

	box := pane("", rows.String(), m.width)

	// lipgloss.Place centers (or left-aligns) each LINE of the string handed
	// to it independently, sized against that string's widest line. Handing
	// it box+status+footer concatenated as-is would make every shorter line
	// drift toward center on its own, which is the "justified" look this
	// used to have. JoinVertical pads every line of every piece out to the
	// widest piece's width first, so the result is one rectangle that moves
	// as a whole once it's centered in the terminal.
	//
	// Center, not Left: the footer is much wider than the box, so a left join
	// pins the box to the footer's left edge — leaving it visibly off-center
	// under the centered banner.
	parts := []string{box, ""}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	v := m.current()
	sshAvailable := v != nil && qemu.Running(v)
	parts = append(parts, renderFooter(listHelp{sshAvailable: sshAvailable}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}
