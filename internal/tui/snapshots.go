package tui

import (
	"fmt"
	"io"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/theme"
)

// snapshotsModal is the "S" key's front end for core.Snapshot, core.Restore,
// core.DeleteSnapshot and core.Snapshots (internal/core/snapshot.go). Each
// action is one core call. A successful action re-reads core.Snapshots so
// the list matches reality.
//
// viewDetail renders this modal inline. app.go's renderModal only handles
// *imageModal, so this file avoids app.go entirely.
//
// Rows key off Tag, not ID. A running VM's `info snapshots` prints "--" for
// ID, and core.Snapshot has no ID field (see parseSnapshots in
// internal/core/snapshot.go). core.Restore, core.DeleteSnapshot and
// core.TakeSnapshot all take Tag too.
type snapshotsModal struct {
	vmName string
	list   list.Model

	// taking is non-nil only while the "take a new snapshot" tag prompt is
	// open. A pointer, not a bare textinput.Model, so nothing has to invent a
	// second "is this open" flag alongside it.
	taking *textinput.Model

	// pendingDelete and pendingRestore hold the snapshot awaiting a y/N
	// answer. "y" confirms, any other key cancels, the same rule as
	// list.go's pendingDelete and app.go's pendingProvision.
	//
	// Restore discards everything since the snapshot was taken, with no way
	// back. That makes it as destructive as delete, so it gets the same gate.
	//
	// Two fields, not one shared field with a "which action" tag. Only one is
	// ever armed at a time. Mirrors app.go's own pendingDelete/pendingProvision
	// split.
	pendingDelete  *core.Snapshot
	pendingRestore *core.Snapshot

	err string // set on a refused action; cleared the next time one succeeds
}

// snapshotsOpenedMsg carries the result of the "S" key's initial
// core.Snapshots(name) read. The read runs off the UI goroutine, like every
// other blocking core call in this package (openLogPager,
// typeConsolePassword).
//
// err is core's own refusal, for example ErrNoDisk for a live-mode VM. This
// package does not pre-guess which VMs can snapshot.
type snapshotsOpenedMsg struct {
	name  string
	snaps []core.Snapshot
	err   error
}

func openSnapshots(name string) tea.Cmd {
	return func() tea.Msg {
		snaps, err := core.Snapshots(name)
		return snapshotsOpenedMsg{name: name, snaps: snaps, err: err}
	}
}

// snapshotsRefreshedMsg carries the result of a take/restore/delete Cmd: the
// mutation runs, then on success a fresh core.Snapshots read runs too. err is
// whichever step failed. On a mutation failure the re-read never runs, so the
// list stays as it was.
type snapshotsRefreshedMsg struct {
	snaps []core.Snapshot
	err   error
}

// snapshotAction runs action (a take/restore/delete against name) and, on
// success, re-reads core.Snapshots(name) so the list reflects what just
// happened. Both legs shell out to qemu-img or dial QMP, so this always runs
// off the UI goroutine.
func snapshotAction(name string, action func() error) tea.Cmd {
	return func() tea.Msg {
		if err := action(); err != nil {
			return snapshotsRefreshedMsg{err: err}
		}
		snaps, err := core.Snapshots(name)
		return snapshotsRefreshedMsg{snaps: snaps, err: err}
	}
}

func newSnapshotsModal(name string, snaps []core.Snapshot) *snapshotsModal {
	sm := &snapshotsModal{vmName: name, list: newSnapshotList()}
	sm.setSnaps(snaps)
	return sm
}

// newSnapshotList builds the modal's list component. Filtering is off, the
// same as newImageList: a VM's own snapshots are a handful of rows, not a
// catalog worth searching. SetStatusBarItemName gives an empty VM
// list.Model's own "No snapshots." empty state, instead of a hand-written
// one.
func newSnapshotList() list.Model {
	l := list.New(nil, snapshotDelegate{}, modalContentWidth, modalRows)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetStatusBarItemName("snapshot", "snapshots")
	l.Styles = listStyles(l.Styles)
	return l
}

func (sm *snapshotsModal) setSnaps(snaps []core.Snapshot) {
	items := make([]list.Item, len(snaps))
	for i, s := range snaps {
		items[i] = snapshotItem{s}
	}
	sm.list.SetItems(items)
}

// selected reports the snapshot under the cursor. False for an empty list,
// so "r"/"d" on a VM with no snapshots do nothing instead of acting on a
// zero-value core.Snapshot.
func (sm *snapshotsModal) selected() (core.Snapshot, bool) {
	it, ok := sm.list.SelectedItem().(snapshotItem)
	if !ok {
		return core.Snapshot{}, false
	}
	return it.snap, true
}

// update handles one message while the modal is open. It returns a Cmd and
// whether the modal should close. This mirrors imageModal.update's (cmd,
// closed) shape, so detail.go's sub-mode intercept (see updateDetail) treats
// this modal the same as the log pager beside it.
func (sm *snapshotsModal) update(msg tea.Msg) (tea.Cmd, bool) {
	// A mutation's result reaches here regardless of sub-mode. taking,
	// pendingDelete and pendingRestore all clear before issuing the Cmd
	// that produces this message. By the time it arrives, the modal is
	// back at the plain list.
	if r, ok := msg.(snapshotsRefreshedMsg); ok {
		if r.err != nil {
			sm.err = r.err.Error()
			return nil, false
		}
		sm.err = ""
		sm.setSnaps(r.snaps)
		return nil, false
	}

	if sm.taking != nil {
		return sm.updateTaking(msg)
	}
	if sm.pendingDelete != nil {
		return sm.updatePendingConfirm(msg, &sm.pendingDelete, func(tag string) error {
			return core.DeleteSnapshot(sm.vmName, tag)
		})
	}
	if sm.pendingRestore != nil {
		return sm.updatePendingConfirm(msg, &sm.pendingRestore, func(tag string) error {
			return core.Restore(sm.vmName, tag)
		})
	}

	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		sm.list, cmd = sm.list.Update(msg)
		return cmd, false
	}

	switch key.String() {
	case "esc":
		return nil, true
	case "n":
		ti := theme.TextInput()
		ti.Placeholder = "tag"
		ti.SetWidth(sm.list.Width())
		sm.taking = &ti
		sm.err = ""
		return ti.Focus(), false
	case "r":
		s, ok := sm.selected()
		if !ok {
			return nil, false
		}
		sm.pendingRestore = &s
		sm.err = ""
		return nil, false
	case "d":
		s, ok := sm.selected()
		if !ok {
			return nil, false
		}
		sm.pendingDelete = &s
		sm.err = ""
		return nil, false
	}

	var cmd tea.Cmd
	sm.list, cmd = sm.list.Update(msg)
	return cmd, false
}

// updateTaking owns the keyboard while the tag prompt is open. enter takes
// the snapshot with whatever was typed. core rejects an empty or whitespace
// tag (see core.snapshotTarget); this function does not duplicate that
// check. esc cancels back to the list without taking anything.
func (sm *snapshotsModal) updateTaking(msg tea.Msg) (tea.Cmd, bool) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			sm.taking = nil
			return nil, false
		case "enter":
			tag := sm.taking.Value()
			sm.taking = nil
			return snapshotAction(sm.vmName, func() error { return core.TakeSnapshot(sm.vmName, tag) }), false
		}
	}
	var cmd tea.Cmd
	ti, cmd := sm.taking.Update(msg)
	sm.taking = &ti
	return cmd, false
}

// updatePendingConfirm owns the keyboard while either destructive action
// awaits confirmation. "y" confirms, any other key cancels, the same rule
// list.go's own delete prompt uses.
//
// pendingDelete and pendingRestore shared this logic before, as two
// functions differing only in the tag. This one function keeps a future
// change to the confirm/cancel rule from landing on one leg and not the
// other.
//
// pending points at the modal's own pendingDelete or pendingRestore field,
// not a copy. Clearing *pending here disarms the prompt on the modal. act is
// the core call to make with the confirmed snapshot's tag.
func (sm *snapshotsModal) updatePendingConfirm(msg tea.Msg, pending **core.Snapshot, act func(tag string) error) (tea.Cmd, bool) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	tag := (*pending).Tag
	*pending = nil
	if key.String() != "y" {
		return nil, false
	}
	return snapshotAction(sm.vmName, func() error { return act(tag) }), false
}

// resize fits the modal to the terminal: fixed chrome minus margin, the
// same shape logPager.resize uses, for the same reason. This file cannot add
// a message route in app.go, so viewDetail calls resize on every render
// instead of on tea.WindowSizeMsg.
func (sm *snapshotsModal) resize(termWidth, termHeight int) {
	w := termWidth - paneFrame() - snapshotsMargin
	if w < 1 {
		w = 1
	}
	rows := termHeight - snapshotsChrome
	if n := len(sm.list.Items()); n > 0 && rows > n {
		rows = n
	}
	if rows < 1 {
		rows = 1
	}
	sm.list.SetWidth(w)
	sm.list.SetHeight(rows)
	sm.list.SetShowPagination(len(sm.list.Items()) > rows)
	if sm.taking != nil {
		sm.taking.SetWidth(w)
	}
}

// snapshotsChrome is the vertical space the pane spends on everything but
// list rows: border, padding, title, the hint line, and one status line
// (tag prompt, delete/restore confirmation, or error) above it. Mirrors
// modalHeightChrome and logPagerChrome for the other two panels.
const snapshotsChrome = 8

// snapshotsMargin keeps the pane off the terminal's edges, the same call
// logPagerMargin makes.
const snapshotsMargin = 4

// view renders the modal: the list, one status line (whichever of the tag
// prompt, the delete confirmation or an error is active), then the hint.
func (sm *snapshotsModal) view(width int) string {
	title := "snapshots" + glyphSep + sm.vmName
	hint := dimStyle.Render("n take · r restore · d delete · esc close")

	var status string
	switch {
	case sm.taking != nil:
		hint = dimStyle.Render("enter take · esc cancel")
		status = sm.taking.View() + "\n"
	case sm.pendingDelete != nil:
		hint = dimStyle.Render("y confirm · any other key cancels")
		status = warnStyle.Render("delete "+sm.pendingDelete.Tag+"? y/N") + "\n"
	case sm.pendingRestore != nil:
		hint = dimStyle.Render("y confirm · any other key cancels")
		status = warnStyle.Render("restore "+sm.pendingRestore.Tag+"? y/N") + "\n"
	case sm.err != "":
		status = errStyle.Render(sm.err) + "\n"
	}

	body := lipgloss.NewStyle().Width(sm.list.Width()).
		Render(sm.list.View() + "\n\n" + status + hint)
	return pane(title, body, width)
}

// snapshotItem is one row: a core.Snapshot. FilterValue is unused (filtering
// is off, same reasoning newImageList gives), kept only to satisfy
// list.Item.
type snapshotItem struct{ snap core.Snapshot }

func (i snapshotItem) FilterValue() string { return i.snap.Tag }

// snapshotTagWidth is the tag column: a fixed label width, the same
// convention imageDelegate's variant rows use, so the size and state columns
// that follow line up down the list.
const snapshotTagWidth = 20

// snapshotDelegate renders one row: tag, size, whether the guest's memory
// was captured, and when it was taken.
type snapshotDelegate struct{}

func (snapshotDelegate) Height() int                         { return 1 }
func (snapshotDelegate) Spacing() int                        { return 0 }
func (snapshotDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d snapshotDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(snapshotItem)
	if !ok {
		return
	}
	cursor := "  "
	if index == m.Index() {
		cursor = selStyle.Render(glyphCursor)
	}
	label := fmt.Sprintf("%-*s", snapshotTagWidth, it.snap.Tag)
	if index == m.Index() {
		label = selStyle.Render(label)
	}
	// VMState distinguishes a snapshot taken while running (disk + RAM,
	// resumes execution) from one taken while stopped (disk only, boots
	// normally); see core.Snapshot's own doc. Both are useful, so the state
	// is shown rather than left implicit.
	state := dimStyle.Render("disk only")
	if it.snap.VMState {
		state = upStyle.Render("disk+ram")
	}
	size := dimStyle.Render(fmt.Sprintf("%-9s", it.snap.Size))
	fmt.Fprint(w, cursor+label+size+state+"  "+dimStyle.Render(it.snap.Created))
}
