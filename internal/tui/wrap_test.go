package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A binding that does not fit moves to the next line; none is cut.
func TestFooterWrapsInsteadOfTruncating(t *testing.T) {
	for w := 60; w <= 120; w += 4 {
		for _, km := range []help.KeyMap{listHelp{sshAvailable: true}, detailHelp{sshAvailable: true}, formHelp{}, editHelp{}} {
			for _, all := range []bool{false, true} {
				out := ansi.Strip(renderFooter(km, w, all))
				if strings.Contains(out, "…") {
					t.Errorf("width %d, all=%v, %T: footer was truncated:\n%s", w, all, km, out)
				}
				for _, l := range strings.Split(out, "\n") {
					if lipgloss.Width(l) > w {
						t.Errorf("width %d, all=%v, %T: line is %d cells:\n%s", w, all, km, lipgloss.Width(l), out)
					}
				}
				bindings := km.ShortHelp()
				if all {
					bindings = nil
					for _, g := range km.FullHelp() {
						bindings = append(bindings, g...)
					}
				}
				for _, b := range bindings {
					if d := ansi.Strip(b.Help().Desc); b.Enabled() && !strings.Contains(out, d) {
						t.Errorf("width %d, all=%v, %T: %q missing from footer:\n%s", w, all, km, d, out)
					}
				}
			}
		}
	}
}

func TestPaneExpandsTabs(t *testing.T) {
	out := pane("", "console\tpass\nx", 0)
	if strings.Contains(out, "\t") {
		t.Errorf("pane kept a literal tab:\n%q", out)
	}
	lines := strings.Split(out, "\n")
	for _, l := range lines {
		if lipgloss.Width(l) != lipgloss.Width(lines[0]) {
			t.Errorf("ragged border, %d vs %d cells:\n%s", lipgloss.Width(l), lipgloss.Width(lines[0]), out)
		}
	}
}

// The logo takes seven rows, so a terminal shorter than bannerMinHeight gets
// the rows for VMs instead. Widths 60 and 80 also wrap the footer onto a
// second line, which must still fit.
func TestBannerHiddenOnShortTerminals(t *testing.T) {
	for _, c := range []struct {
		height     int
		wantBanner bool
	}{
		{20, false}, {24, false}, {bannerMinHeight - 1, false}, {bannerMinHeight, true}, {50, true},
	} {
		for _, width := range []int{60, 80, 100} {
			m := model{screen: screenList, list: newVMList(), provisioning: map[string]provState{}}
			mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: c.height})
			m = mm.(model)
			mm, _ = m.Update(vmsLoadedMsg{vms: geoVMs(t, 30)})
			m = mm.(model)

			out := m.View().Content
			if got := strings.Contains(out, "███████╗"); got != c.wantBanner {
				t.Errorf("%dx%d: banner shown = %v, want %v", width, c.height, got, c.wantBanner)
			}
			if got := lipgloss.Height(out); got > c.height {
				t.Errorf("%dx%d rendered %d lines, the footer is cut off", width, c.height, got)
			}
		}
	}
}
