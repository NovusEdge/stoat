package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/core"
)

// The list shows every VM in one sequence, broken ones (a directory whose
// vm.toml won't parse) among them. current and currentBroken split the
// selected row by state: exactly one of them returns non-nil for any valid
// selection, so a caller cannot forget that a row might be broken. Both read
// through the list component, so they honour an active search filter:
// indexing m.vms directly would select the wrong VM whenever one is applied.
func (m model) current() *core.VM {
	it, ok := m.selectedItem()
	if !ok || it.vm.State == core.StateBroken {
		return nil
	}
	v := it.vm
	return &v
}

func (m model) currentBroken() *core.VM {
	it, ok := m.selectedItem()
	if !ok || it.vm.State != core.StateBroken {
		return nil
	}
	v := it.vm
	return &v
}

func startVM(v core.VM) tea.Cmd {
	return func() tea.Msg {
		// A live VM's root is wiped by the boot about to happen, so a log from
		// its previous life describes work that no longer exists.
		ensureNoStaleLog(v)
		// v.Name is the DIRECTORY, not the vm.toml name field; see the identity
		// note on core.VM.Name.
		if err := core.Start(v.Name); err != nil {
			return errMsg(err.Error())
		}
		return vmStartedMsg{v}
	}
}

// vmStartedMsg reports a successful start, carrying the VM so the model can
// decide whether to watch for ssh and offer to provision.
type vmStartedMsg struct{ vm core.VM }

func stopVM(v core.VM) tea.Cmd {
	return func() tea.Msg {
		if err := core.Stop(v.Name); err != nil {
			return errMsg(err.Error())
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
	key, ok := msg.(tea.KeyPressMsg)
	if !ok || m.filterActive() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.syncListHeight()
		return m, cmd
	}

	// The auto-provision offer owns all keys while pending, for the same
	// reason the delete prompt below does: "n" is bound to new-VM, so a
	// plain key switch would open the form on a decline.
	if m.pendingProvision != nil {
		v := *m.pendingProvision
		m.pendingProvision = nil
		if key.String() == "y" {
			return m, m.startProvision(v)
		}
		cmd := m.showToast("not provisioning "+v.Name+", press p when you want to", false)
		return m, cmd
	}

	// The delete confirmation prompt owns all keys while pending: "y"
	// confirms, anything else cancels. This must run before the normal
	// switch below because "n" is otherwise bound to "new VM".
	if m.pendingDelete != nil {
		if key.String() == "y" {
			v := *m.pendingDelete
			m.pendingDelete = nil
			m.status = ""
			return m, tea.Sequence(deleteVM(v), loadVMs)
		}
		m.pendingDelete = nil
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
		// component: it owns the cursor and the filter.
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.syncListHeight()
		return m, cmd
	case "esc":
		// Clears an applied filter. Note a pending delete is handled earlier
		// and consumes esc before this runs, so with both active the delete
		// cancels and the filter stays, the safer of the two orderings.
		if m.list.IsFiltered() {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			m.syncListHeight()
			return m, cmd
		}
		m.pendingDelete = nil
		m.status = ""
		return m, nil
	case "enter":
		if v == nil {
			if broken != nil {
				cmd := m.showToast(broken.Name+": broken vm.toml, cannot start (d to delete)", true)
				return m, cmd
			}
			break
		}
		m.status = ""
		if v.State == core.StateRunning {
			return m, tea.Sequence(stopVM(*v), loadVMs)
		}
		// No loadVMs here: startVM's vmStartedMsg handler issues one itself,
		// alongside the ssh watch, so sequencing another would refresh twice.
		return m, startVM(*v)
	case "r":
		return m, openRecipesDir()
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
			// Re-fetch rather than reuse the list's row: *v is a
			// point-in-time snapshot from the last loadVMs, and detail's
			// State/Applied must be current the moment the user opens this
			// VM, not whenever the list last refreshed. v.Name is the
			// directory, which is the key core.Get takes.
			cv, err := core.Get(v.Name)
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			m.detail = newDetail(cv)
			m.screen = screenDetail
			m.detailGen++
			m.showHelp = false
			return m, tick(m.detailGen)
		}
		if broken != nil {
			cmd := m.showToast(broken.Name+": broken vm.toml, cannot start (d to delete)", true)
			return m, cmd
		}
	case "s":
		if v != nil && v.State == core.StateRunning {
			return m, sshInto(*v)
		}
		cmd := m.showToast("not running", true)
		return m, cmd
	case "p":
		if v != nil {
			return m, m.startProvision(*v)
		}
	case "d":
		// One prompt for both kinds of row: core.Destroy takes a directory
		// name and handles a broken VM the same as a good one, including the
		// still-running check a broken row used to skip.
		switch {
		case v != nil:
			if v.State == core.StateRunning {
				cmd := m.showToast("stop "+v.Name+" first", true)
				return m, cmd
			}
			m.status = "delete " + v.Name + "? y/N"
			m.screen = screenList
			m.pendingDelete = v
		case broken != nil:
			m.status = "delete " + broken.Name + "? y/N"
			m.screen = screenList
			m.pendingDelete = broken
		}
	}
	return m, nil
}

// deleteVM removes a VM's directory through core.Destroy. It takes broken VMs
// too: Destroy accepts one the same as a good one and, unlike the separate
// broken-row path this replaces, checks first whether a qemu process is still
// running against that directory. That old path reimplemented the data-root
// containment check by hand and skipped the running check entirely, so
// deleting a broken row could rip the directory, pidfile, monitor socket and
// disk out from under a live qemu; see core.Destroy's comment for the
// incident.
//
// v.Name is the DIRECTORY, which is the identity core.Destroy acts on, never
// the vm.toml name field that can diverge from it; see core.VM's identity
// note. TestDeleteTargetsDirectoryNotName pins this.
func deleteVM(v core.VM) tea.Cmd {
	return func() tea.Msg {
		if err := core.Destroy(v.Name); err != nil {
			return errMsg(err.Error())
		}
		return statusMsg(v.Name + " deleted")
	}
}

// brokenReason returns a short, single-line reason string for a broken
// vm.toml's parse error, suitable for a list row.
func brokenReason(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 60
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}

// viewList renders the list screen's body: everything below the banner,
// which View composes separately so it can be centered over this block
// instead of sharing its left edge (see View's doc comment). The VM rows
// live inside a pane; the status line and footer sit outside it, left-
// aligned to the pane's own left edge.
func (m model) viewList() string {
	var rows strings.Builder

	if m.preflight != "" {
		rows.WriteString(errStyle.Render(m.preflight) + "\n\n")
	}
	switch {
	case len(m.list.Items()) == 0:
		// The genuinely-empty first run. Left to the component this renders
		// its own "No vms." (capitalised, full stop, no guidance) as the
		// very first screen a new user ever sees.
		rows.WriteString(dimStyle.Render("no vms yet, press n to create one"))
	// "there are VMs but none are visible" rather than IsFiltered(), which
	// only reports an APPLIED filter. While the user is still typing, the
	// state is Filtering and the pane would otherwise render empty.
	case len(m.list.VisibleItems()) == 0:
		// bubbles/list renders an empty viewport here; without a message the
		// pane just collapses and reads as "stoat lost my VMs".
		rows.WriteString(dimStyle.Render("no vm matches this search, esc clears it"))
	default:
		// The delegate puts a blank line after every row including the last;
		// inside a padded pane that reads as a stray gap above the border.
		rows.WriteString(strings.TrimRight(m.list.View(), " \n"))
	}

	// Fixed width, like the form: the pane must not resize as VM names
	// change length or a search empties it.
	box := paneAt("", rows.String(), listWidth, m.width)
	// How to get into the selected VM, beside the list rather than a screen
	// away: the answer differs per VM, and guessing wrong leaves you at a
	// login prompt. Dropped entirely on a terminal too narrow for both.
	cur := m.current()
	ci := ""
	if cur != nil {
		ci = m.cloudInit[cur.Name]
	}
	box = joinAccess(box, accessBox(cur, ci, m.ciProg, m.width), m.width)

	// lipgloss.Place centers (or left-aligns) each LINE of the string handed
	// to it independently, sized against that string's widest line. Handing
	// it box+status+footer concatenated as-is would make every shorter line
	// drift toward center on its own, which is the "justified" look this
	// used to have. JoinVertical pads every line of every piece out to the
	// widest piece's width first, so the result is one rectangle that moves
	// as a whole once it's centered in the terminal.
	//
	// Center, not Left: the footer is much wider than the box, so a left join
	// pins the box to the footer's left edge, leaving it visibly off-center
	// under the centered banner.
	parts := []string{box, ""}
	// The search line sits between the pane and the status: while the input
	// is open it IS the input, and once a filter is applied it reports what
	// is being hidden. Without it a filtered list just looks like VMs went
	// missing.
	if search := listStatusLine(m.list); search != "" {
		parts = append(parts, search, "")
	}
	// In-flight provision runs sit above the status line: they are ongoing
	// state, not a one-off message, and a run started from here keeps going
	// while the user moves around the list.
	for _, l := range provLines(m) {
		parts = append(parts, l)
	}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	v := m.current()
	sshAvailable := v != nil && v.State == core.StateRunning
	parts = append(parts, renderFooter(listHelp{sshAvailable: sshAvailable}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}
