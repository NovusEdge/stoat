package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/config"
)

// drainCmds applies msg and then runs the commands it returns, because
// bubbles/list resolves filtering asynchronously — without this the filter
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
		t.Errorf("selected %q, want gamma — an action would hit the wrong VM", v.Name)
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
// the selection to the FIRST one — and the next "d" would arm a delete on the
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
// component this renders its own "No vms." — capitalised, full stop, no
// guidance — as the very first screen a new user ever sees.
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
