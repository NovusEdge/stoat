package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/core"
)

// These tests exist because a whole-branch review found three geometry bugs
// that a fully green suite had not noticed: rows wrapping, the pane running a
// line past the terminal and cutting off the footer, and the footer coming
// back wider than the screen. Every other test in this package asserts
// behaviour; nothing measured what was actually drawn.

func geoVMs(t *testing.T, n int) []core.VM {
	t.Helper()
	out := make([]core.VM, n)
	for i := range out {
		out[i] = core.VM{
			Name: fmt.Sprintf("vm%02d", i), Mode: "cloud", State: core.StateStopped,
			RAM: 8192, CPUs: 8, SSHPort: 2200 + i, Paths: core.Paths{Dir: t.TempDir()},
		}
	}
	return out
}

// TestListPaneFitsTheTerminal pins the pagination arithmetic. Budgeting two
// lines for the pagination row (it costs one: bubbles only adds a top margin
// when the delegate's spacing is 0, and ours is 1) made the block taller than
// the terminal, which flips View() to top-align and lets the terminal eat the
// bottom line: the help footer.
func TestListPaneFitsTheTerminal(t *testing.T) {
	cases := []struct{ height, vms int }{
		{20, 0}, {20, 1}, {20, 8}, {20, 50},
		{24, 8}, {24, 50},
		{30, 50}, {34, 50}, {40, 3}, {60, 50},
	}
	for _, c := range cases {
		m := model{screen: screenList, list: newVMList(), provisioning: map[string]provState{}}
		mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: c.height})
		m = mm.(model)
		mm, _ = m.Update(vmsLoadedMsg{vms: geoVMs(t, c.vms)})
		m = mm.(model)

		if got := lipgloss.Height(m.View().Content); got > c.height {
			t.Errorf("%d vms at height %d rendered %d lines, the footer is cut off",
				c.vms, c.height, got)
		}
	}
}

// TestListWidthFitsARunningRow is the test that would have caught the worst
// finding. Every render in the suite showed STOPPED VMs, whose state column is
// a single "-" (38 cells); a running one carries "up 1h30m0s  :2200" and needs
// 54, so the pane wrapped the port onto its own line the moment anything was
// actually up. Pure arithmetic on the row format, so it needs no live qemu.
func TestListWidthFitsARunningRow(t *testing.T) {
	// The widest state string the detail row can hold: a VM up for just under
	// 1000 hours, with a 4-digit port.
	worst := "  " + "● " +
		fmt.Sprintf("%-14s %-5s %5dM %2dc  ", strings.Repeat("n", 14), "cloud", 65536, 99) +
		"up 999h59m59s  :65535"

	if len(worst) > listWidth {
		t.Errorf("listWidth = %d but the widest running row is %d cells; rows will wrap:\n%s",
			listWidth, len(worst), worst)
	}
}

// TestFooterNeverOverflows covers help.Model giving up on truncation: once its
// running total passes the width it can no longer fit an ellipsis, so it
// appends every remaining binding and returns a line WIDER than the terminal,
// which then wraps and pushes the whole screen up. 60 is the enforced floor
// and 80 the classic default, and both were broken.
func TestFooterNeverOverflows(t *testing.T) {
	for w := 60; w <= 120; w += 2 {
		for _, km := range []help.KeyMap{
			listHelp{}, listHelp{sshAvailable: true},
			detailHelp{}, detailHelp{sshAvailable: true}, formHelp{}, editHelp{},
		} {
			if got := lipgloss.Width(renderFooter(km, w, false)); got > w {
				t.Errorf("footer at width %d rendered %d cells for %T", w, got, km)
			}
		}
	}
}
