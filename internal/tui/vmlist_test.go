package tui

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
)

// drainCmds applies msg and then runs the commands it returns, because
// bubbles/list resolves filtering asynchronously, so without this the filter
// never actually applies and every assertion below would pass vacuously.
//
// The bound is 2, not "until nil": the component also returns a spinner tick
// whose command genuinely sleeps, so draining further would add seconds per
// call. Two rounds is enough for the filter to resolve.
func drainCmds(m model, msg tea.Msg) model {
	mm, cmd := m.Update(msg)
	m = mm.(model)
	for i := 0; cmd != nil && i < 2; i++ {
		out := cmd()
		if out == nil {
			break
		}
		if batch, ok := out.(tea.BatchMsg); ok {
			for _, c := range batch {
				if sub := c(); sub != nil {
					mm, _ = m.Update(sub)
					m = mm.(model)
				}
			}
			break
		}
		mm, cmd = m.Update(out)
		m = mm.(model)
	}
	return m
}

func listFixture(t *testing.T) model {
	t.Helper()
	vms := []*config.VM{
		{Name: "alpha", Mode: "live", RAM: 4096, CPUs: 4, SSHPort: 2200, Dir: t.TempDir()},
		{Name: "beta", Mode: "disk", RAM: 2048, CPUs: 2, SSHPort: 2201, Dir: t.TempDir()},
		{Name: "gamma", Mode: "cloud", RAM: 8192, CPUs: 8, SSHPort: 2202, Dir: t.TempDir()},
	}
	m := model{screen: screenList, list: newVMList(), provisioning: map[string]provState{}}
	m = drainCmds(m, tea.WindowSizeMsg{Width: 100, Height: 34})
	return drainCmds(m, vmsLoadedMsg{vms: vms})
}

func typeSearch(m model, term string) model {
	m = drainCmds(m, keyMsg("/"))
	for _, r := range term {
		m = drainCmds(m, keyMsg(string(r)))
	}
	return m
}

// TestFilteredSelectionActsOnTheVisibleRow is the one that really matters.
// Every action key (enter/start, d/delete, p/provision) resolves the target
// through m.current(). If that indexed the unfiltered slice while a filter
// was applied, pressing d on the only visible row would delete a DIFFERENT
// VM than the one under the cursor.
func TestFilteredSelectionActsOnTheVisibleRow(t *testing.T) {
	m := typeSearch(listFixture(t), "gam")
	m = drainCmds(m, keyMsg("enter")) // apply the filter

	if got := len(m.list.VisibleItems()); got != 1 {
		t.Fatalf("filter %q left %d visible rows, want 1", "gam", got)
	}
	v := m.current()
	if v == nil {
		t.Fatal("no VM selected under an applied filter")
	}
	if v.Name != "gamma" {
		t.Errorf("selected %q, want gamma; an action would hit the wrong VM", v.Name)
	}
}

// TestSearchKeysDoNotFireBindings covers the collision that makes a search
// box dangerous in this UI: "d" is delete and "n" is new-VM, so typing a name
// containing them must not trigger either.
func TestSearchKeysDoNotFireBindings(t *testing.T) {
	m := typeSearch(listFixture(t), "dn")

	if m.screen != screenList {
		t.Errorf("typing 'n' into search left screen %v, want the list", m.screen)
	}
	if m.pendingDelete != nil || m.pendingDeleteBroken != "" {
		t.Error("typing 'd' into search armed a delete confirmation")
	}
	if !m.filterActive() {
		t.Error("filter input should still be open")
	}
	if got := m.list.FilterValue(); got != "dn" {
		t.Errorf("filter text = %q, want %q", got, "dn")
	}
}

// TestEscClearsFilterBeforeAnythingElse pins the escape ordering: with a
// filter applied, esc must restore the full list rather than silently doing
// nothing visible.
func TestEscClearsFilterBeforeAnythingElse(t *testing.T) {
	m := typeSearch(listFixture(t), "alp")
	m = drainCmds(m, keyMsg("enter"))
	if len(m.list.VisibleItems()) != 1 {
		t.Fatalf("filter did not apply: %d visible", len(m.list.VisibleItems()))
	}

	m = drainCmds(m, keyMsg("esc"))
	if m.list.IsFiltered() {
		t.Error("esc left the filter applied")
	}
	if got := len(m.list.VisibleItems()); got != 3 {
		t.Errorf("after esc %d rows visible, want all 3", got)
	}
}

// TestNoMatchesStillRendersAPane covers the case that otherwise reads as
// data loss: a search matching nothing collapsed the pane to an empty box,
// with no hint that a filter was responsible.
func TestNoMatchesStillRendersAPane(t *testing.T) {
	m := typeSearch(listFixture(t), "zzzz")
	if got := len(m.list.VisibleItems()); got != 0 {
		t.Fatalf("%q matched %d rows, want 0", "zzzz", got)
	}
	out := m.viewList()
	if !strings.Contains(out, "no vm matches") {
		t.Errorf("empty search result has no explanation:\n%s", out)
	}
	if !strings.Contains(out, "esc") {
		t.Error("empty search result doesn't say how to get out")
	}
}

// TestAllVMsFitOnOnePage guards the pagination arithmetic: the component
// derives items-per-page from height/(itemHeight+spacing), so an off-by-one
// in listHeight silently strands the last VM on a second page.
func TestAllVMsFitOnOnePage(t *testing.T) {
	m := listFixture(t)
	out := m.viewList()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, name) {
			t.Errorf("%q is not on the first page:\n%s", name, out)
		}
	}
}

// TestRefreshKeepsFilterApplied covers the periodic reload: SetItems returns
// a Cmd that re-applies the filter, and dropping it would leave the list
// showing stale matches after a VM starts or stops.
func TestRefreshKeepsFilterApplied(t *testing.T) {
	m := typeSearch(listFixture(t), "bet")
	m = drainCmds(m, keyMsg("enter"))

	vms := []*config.VM{
		{Name: "alpha", Mode: "live", RAM: 4096, CPUs: 4, SSHPort: 2200, Dir: t.TempDir()},
		{Name: "beta", Mode: "disk", RAM: 2048, CPUs: 2, SSHPort: 2201, Dir: t.TempDir()},
		{Name: "gamma", Mode: "cloud", RAM: 8192, CPUs: 8, SSHPort: 2202, Dir: t.TempDir()},
		{Name: "delta", Mode: "cloud", RAM: 1024, CPUs: 1, SSHPort: 2203, Dir: t.TempDir()},
	}
	m = drainCmds(m, vmsLoadedMsg{vms: vms})

	if !m.list.IsFiltered() {
		t.Fatal("refresh dropped the filter")
	}
	if got := len(m.list.VisibleItems()); got != 1 {
		t.Errorf("after refresh %d rows visible, want 1 (beta)", got)
	}
	if v := m.current(); v == nil || v.Name != "beta" {
		t.Errorf("selection after refresh = %v, want beta", v)
	}
}

// TestCursorClampsWhenTheBottomVMDisappears covers a subtle one: SetItems does
// not clamp the cursor, and the SetHeight that follows remaps an out-of-range
// index to the TOP of the list rather than the bottom (it recomputes
// page/cursor against a new, smaller PerPage). So deleting the last VM moved
// the selection to the FIRST one, and the next "d" would arm a delete on the
// wrong VM. This repo has shipped a delete-the-wrong-VM bug before.
func TestCursorClampsWhenTheBottomVMDisappears(t *testing.T) {
	m := listFixture(t) // alpha, beta, gamma
	m = drainCmds(m, keyMsg("j"))
	m = drainCmds(m, keyMsg("j"))
	if v := m.current(); v == nil || v.Name != "gamma" {
		t.Fatalf("setup: selected %v, want gamma", v)
	}

	// gamma is deleted; the refresh brings back two VMs.
	m = drainCmds(m, vmsLoadedMsg{vms: []*config.VM{
		{Name: "alpha", Mode: "live", RAM: 4096, CPUs: 4, SSHPort: 2200, Dir: t.TempDir()},
		{Name: "beta", Mode: "disk", RAM: 2048, CPUs: 2, SSHPort: 2201, Dir: t.TempDir()},
	}})

	v := m.current()
	if v == nil {
		t.Fatal("nothing selected after the bottom VM disappeared")
	}
	if v.Name != "beta" {
		t.Errorf("selection jumped to %q; want beta (the new bottom row), not the top", v.Name)
	}
}

// TestFirstRunSaysHowToStart guards the empty-state copy. Left to the
// component this renders its own "No vms." (capitalised, full stop, no
// guidance) as the very first screen a new user ever sees.
func TestFirstRunSaysHowToStart(t *testing.T) {
	m := model{screen: screenList, width: 100, height: 30, list: newVMList()}
	m.syncListHeight()
	out := m.viewList()
	if !strings.Contains(out, "press n to create one") {
		t.Errorf("first-run pane does not say how to start:\n%s", out)
	}
	if strings.Contains(out, "No vms.") {
		t.Error("first-run pane fell through to the component's own empty message")
	}
}

// TestBrokenRowContinuationAlignsUnderText pins the wrap alignment on a
// broken VM's parse error. vmDelegate.Render's plain row for a good VM is
// sized to fit listWidth (TestListWidthFitsARunningRow guards that), but
// brokenReason allows up to 60 characters of TOML error on top of a
// "%-14s broken: " prefix, so a real parse error routinely needs a second
// line. Left to paneAt's own word-wrap over the whole rendered list, the
// continuation used to land flush against the pane's padding, under the
// cursor rather than under the row's own text, and read as a second broken
// entry instead of a continuation of the first.
func TestBrokenRowContinuationAlignsUnderText(t *testing.T) {
	m := model{screen: screenList, width: 80, height: 30, list: newVMList(), provisioning: map[string]provState{}}
	m = drainCmds(m, vmsLoadedMsg{broken: []config.Broken{
		{Name: "old-vm", Err: errors.New("toml: line 1: expected '.' or '=', but got 'i' instead")},
	}})

	var lines []string
	for _, l := range strings.Split(m.viewList(), "\n") {
		lines = append(lines, ansi.Strip(l))
	}

	first := -1
	for i, l := range lines {
		if strings.Contains(l, "old-vm") {
			first = i
			break
		}
	}
	if first == -1 || first+1 >= len(lines) {
		t.Fatalf("broken row did not render, or its error did not wrap:\n%s", strings.Join(lines, "\n"))
	}

	// Columns are counted in runes, not bytes: the pane's border and glyphs
	// (│, ❯, ✗) are multi-byte, so a byte offset would overstate every column
	// past them.
	col := func(s, substr string) int {
		i := strings.Index(s, substr)
		if i < 0 {
			return -1
		}
		return utf8.RuneCountInString(s[:i])
	}

	textCol := col(lines[first], "old-vm")
	border := col(lines[first], "│") // the pane's left border; same column on every row
	if border < 0 {
		t.Fatalf("no left border found on the broken row:\n%s", lines[first])
	}

	// The continuation's column, measured the same way: the first non-space
	// rune after the border, not after column 0 (which would just find the
	// border itself, or the terminal's own left margin).
	contRunes := []rune(lines[first+1])
	contCol := border + 1
	for contCol < len(contRunes) && contRunes[contCol] == ' ' {
		contCol++
	}

	if contCol != textCol {
		t.Errorf("continuation line starts at column %d, want %d (under the row's text, not the cursor):\n%s\n%s",
			contCol, textCol, lines[first], lines[first+1])
	}
}
