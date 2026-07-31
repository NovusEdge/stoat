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

// The list shows good VMs followed by broken ones (a directory whose vm.toml
// won't parse) as one sequence. current and currentBroken split the selected
// row back apart: exactly one of them returns non-nil for any valid
// selection. Both read through the list component, so they honour an active
// search filter — indexing the raw slices would select the wrong VM whenever
// a filter is applied.
func (m model) current() *config.VM {
	it, ok := m.selectedItem()
	if !ok {
		return nil
	}
	return it.vm
}

func (m model) currentBroken() *config.Broken {
	it, ok := m.selectedItem()
	if !ok {
		return nil
	}
	return it.broken
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
	// While the search input is open it owns the keyboard: every one of
	// stoat's single-letter bindings (n, d, p, s, q) is a character the user
	// is trying to type into it. The list component also needs non-key
	// messages (its filter resolves through a Cmd), so those are forwarded
	// before the key switch below ever runs.
	key, ok := msg.(tea.KeyMsg)
	if !ok || m.filterActive() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.syncListHeight()
		return m, cmd
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

	switch key.String() {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
	case "j", "down", "k", "up", "/", "pgup", "pgdown", "home", "end", "g", "G":
		// Movement, paging and opening the search box all belong to the list
		// component — it owns the cursor and the filter.
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.syncListHeight()
		return m, cmd
	case "esc":
		// esc clears an applied filter first; only once there is no filter
		// left does it fall through to cancelling a pending delete.
		if m.list.IsFiltered() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			m.syncListHeight()
			return m, cmd
		}
		m.pendingDelete = nil
		m.pendingDeleteBroken = ""
		m.status = ""
		return m, nil
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
		// A fresh form would reset "fetching" to false while the previous
		// form's download goroutine is still writing, letting a second fetch
		// start on the same file: two writers interleaving into one .part,
		// each verifying its own read stream, producing a corrupt image that
		// passes the checksum. Reuse the form that owns the live download.
		if !m.form.fetching {
			m.form = newForm()
		}
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
	// "there are VMs but none are visible" rather than IsFiltered(), which
	// only reports an APPLIED filter — while the user is still typing, the
	// state is Filtering and the pane would otherwise render empty.
	if len(m.list.VisibleItems()) == 0 && len(m.list.Items()) > 0 {
		// bubbles/list renders an empty viewport here; without a message the
		// pane just collapses and reads as "stoat lost my VMs".
		rows.WriteString(dimStyle.Render("no vm matches this search — esc clears it"))
	} else {
		// The delegate puts a blank line after every row including the last;
		// inside a padded pane that reads as a stray gap above the border.
		rows.WriteString(strings.TrimRight(m.list.View(), " \n"))
	}

	// Fixed width, like the form: the pane must not resize as VM names
	// change length or a search empties it.
	box := paneAt("", rows.String(), listWidth, m.width)

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
	// The search line sits between the pane and the status: while the input
	// is open it IS the input, and once a filter is applied it reports what
	// is being hidden — without it a filtered list just looks like VMs went
	// missing.
	if search := listStatusLine(m.list); search != "" {
		parts = append(parts, search, "")
	}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	v := m.current()
	sshAvailable := v != nil && qemu.Running(v)
	parts = append(parts, renderFooter(listHelp{sshAvailable: sshAvailable}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}
