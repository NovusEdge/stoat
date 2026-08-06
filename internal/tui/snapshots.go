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
// core.DeleteSnapshot and core.Snapshots (internal/core/snapshot.go). No
// logic lives here that core does not already have: every action below is
// one core call, off the UI goroutine, followed by a re-read of
// core.Snapshots so the list reflects whatever actually happened.
//
// It follows logPager's shape, not imagemodal.go's: rendered inline by
// viewDetail rather than composited over the screen, because app.go's
// renderModal is wired specifically to *imageModal and this file has no
// reason to touch app.go to get a second overlay kind. See logPager's own
// comment for the precedent.
//
// Rows never carry or key off a snapshot's ID: a running VM's `info
// snapshots` prints "--" for it, and core.Snapshot has no ID field at all
// (see parseSnapshots' comment in internal/core/snapshot.go). Every action
// here is keyed by Tag instead, the same identity core.Restore,
// core.DeleteSnapshot and core.TakeSnapshot themselves take.
type snapshotsModal struct {
	vmName string
	list   list.Model

	// taking is non-nil only while the "take a new snapshot" tag prompt is
	// open. A pointer, not a bare textinput.Model, so nothing has to invent a
	// second "is this open" flag alongside it.
	taking *textinput.Model

	// pendingDelete and pendingRestore are the snapshot awaiting a y/N
	// answer, the same "y confirms, anything else cancels" rule list.go's
	// pendingDelete and app.go's pendingProvision already use for exactly
	// this kind of prompt. Restore gets the same gate as delete: it discards
	// everything since the snapshot was taken with no way back, which is as
	// destructive as delete even though it doesn't remove the snapshot
	// itself. Two fields rather than one, mirroring app.go's own
	// pendingDelete/pendingProvision split, since only one is ever armed at
	// a time and a shared field would need a second "which one" tag anyway.
	pendingDelete  *core.Snapshot
	pendingRestore *core.Snapshot

	err string // set on a refused action; cleared the next time one succeeds
}

// snapshotsOpenedMsg carries the result of the "S" key's initial
// core.Snapshots(name) read, done off the UI goroutine like every other
// blocking core call in this package (openLogPager, typeConsolePassword).
// err is core's own refusal, e.g. ErrNoDisk for a live-mode VM, surfaced
// rather than pre-guessed which VMs snapshot at all.
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

// snapshotsRefreshedMsg carries the result of a take/restore/delete: the
// mutation, then a fresh core.Snapshots read on success, both in one Cmd. err
// is whichever of the two failed; on a mutation failure the list is left
// exactly as it was, since the re-read never ran.
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
// same call newImageList makes: this list is a handful of a VM's own
// snapshots, not a catalog worth searching. SetStatusBarItemName is what
// gives an empty VM a real "No snapshots." line (list.Model's own empty
// state) instead of a hand-written one.
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

// update handles one message while the modal is open, returning a Cmd and
// whether the modal should close. Mirrors imageModal.update's (cmd, closed)
// shape, which is what lets detail.go's own sub-mode intercept (see
// updateDetail) treat this modal exactly like the log pager already sitting
// beside it.
func (sm *snapshotsModal) update(msg tea.Msg) (tea.Cmd, bool) {
	// A mutation's result reaches here regardless of sub-mode: taking,
	// pendingDelete and pendingRestore all clear themselves before issuing
	// the Cmd that produces this, so by the time it arrives the modal is
	// always back at the plain list.
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
		return sm.updatePendingDelete(msg)
	}
	if sm.pendingRestore != nil {
		return sm.updatePendingRestore(msg)
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
// the snapshot with whatever was typed, letting core reject an empty or
// whitespace tag (see core.snapshotTarget) rather than duplicating that
// validation here; esc cancels back to the list without taking anything.
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

// updatePendingDelete owns the keyboard while a delete awaits confirmation:
// "y" confirms, any other key cancels, the same rule list.go's own delete
// prompt uses.
func (sm *snapshotsModal) updatePendingDelete(msg tea.Msg) (tea.Cmd, bool) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	tag := sm.pendingDelete.Tag
	sm.pendingDelete = nil
	if key.String() != "y" {
		return nil, false
	}
	return snapshotAction(sm.vmName, func() error { return core.DeleteSnapshot(sm.vmName, tag) }), false
}

// updatePendingRestore owns the keyboard while a restore awaits
// confirmation: "y" confirms, any other key cancels, exactly like
// updatePendingDelete above. Restore discards everything the guest has done
// since the snapshot was taken, with no way back, so it gets the same gate.
func (sm *snapshotsModal) updatePendingRestore(msg tea.Msg) (tea.Cmd, bool) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	tag := sm.pendingRestore.Tag
	sm.pendingRestore = nil
	if key.String() != "y" {
		return nil, false
	}
	return snapshotAction(sm.vmName, func() error { return core.Restore(sm.vmName, tag) }), false
}

// resize fits the modal to the terminal, the same fixed-chrome-minus-margin
// shape logPager.resize uses (and for the same reason: this file cannot add
// a message route in app.go, so it is called every render from viewDetail
// rather than on tea.WindowSizeMsg).
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

// snapshotsChrome is the vertical space the pane spends on everything that
// isn't list rows: border, padding, title, the hint line, and the one status
// line (the tag prompt, a delete/restore confirmation, or an error) above
// it. Mirrors modalHeightChrome/logPagerChrome's role for the other two
// panels.
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
	// VMState distinguishes a snapshot taken while the guest was running
	// (disk + RAM, resumes execution) from one taken while stopped (disk
	// only, boots normally); see core.Snapshot's own doc. Both are useful
	// and not interchangeable, so this is shown rather than left implicit.
	state := dimStyle.Render("disk only")
	if it.snap.VMState {
		state = upStyle.Render("disk+ram")
	}
	size := dimStyle.Render(fmt.Sprintf("%-9s", it.snap.Size))
	fmt.Fprint(w, cursor+label+size+state+"  "+dimStyle.Render(it.snap.Created))
}
