package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/core"
)

// provBarWidth and provLabelMax size the compact bar drawn inline in a list
// row: fixed cells, like every other budget in this package, so a package
// name can't push the row wider than the pane it sits under.
const (
	provBarWidth = 10
	provLabelMax = 18
)

// progressNum renders p's count as "N/M" when the source counted packages,
// "NN%" when it only gave a fraction.
func progressNum(p Progress) string {
	if p.Total > 0 {
		return fmt.Sprintf("%d/%d", p.Done, p.Total)
	}
	return fmt.Sprintf("%3.0f%%", p.Frac*100)
}

// progressLabel renders p as the bar plus a number. bar() already draws the
// track with the theme's colors; this adds the number and, when there is
// room, the package name.
func progressLabel(p Progress, width int) string {
	out := bar(p.Frac, width) + " " + dimStyle.Render(progressNum(p))
	if l := p.Label; l != "" {
		out += dimStyle.Render(" · " + ansi.Truncate(l, provLabelMax, "…"))
	}
	return out
}

// detailBarWidth is the bar's cell count in the detail screen's fuller
// panel, wider than provBarWidth since the panel has a pane to itself
// instead of sharing a line with the VM name and elapsed time.
const detailBarWidth = 24

// progressPanel renders the detail screen's install/provision panel for the
// selected VM: phase, bar, current package, elapsed. It returns "" when the
// selected VM is doing neither, so viewDetail can call it unconditionally.
func progressPanel(m model) string {
	v := m.detail.vm
	now := time.Now()

	if v.Mode == "disk" && !v.Installed && v.State == core.StateRunning {
		p, ok := latestInstallProgress(v.Name)
		elapsed := time.Duration(0)
		if !v.StartedAt.IsZero() {
			elapsed = now.Sub(v.StartedAt)
		}
		return renderProgressPanel("installing", p, ok, elapsed, m.width)
	}
	if st, ok := m.provisioning[v.Name]; ok {
		return renderProgressPanel(st.step, st.prog, st.hasProg, now.Sub(st.start), m.width)
	}
	return ""
}

// renderProgressPanel draws the pane shared by both phases: a phase line, a
// bar with the package name when the log has produced one, a "waiting for
// output" line when it hasn't yet, and elapsed time.
func renderProgressPanel(phase string, p Progress, hasProg bool, elapsed time.Duration, width int) string {
	var f fields
	f.row("", "phase", dimStyle.Render(phase))
	if hasProg {
		f.row("", "", bar(p.Frac, detailBarWidth)+" "+dimStyle.Render(progressNum(p)))
		if p.Label != "" {
			f.row("", "package", p.Label)
		}
	} else {
		f.row("", "", dimStyle.Render("waiting for output…"))
	}
	f.row("", "elapsed", provElapsed(elapsed))
	return paneAt("progress", f.String(), appContentWidth, width)
}
