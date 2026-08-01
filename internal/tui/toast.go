package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// A toast reports something that already happened — "created X", "X stopped",
// an error from a command that has finished. It expires on its own, so it
// costs no permanent line and needs no key to dismiss.
//
// Prompts are deliberately NOT toasts. "delete X? y/N" and the auto-provision
// offer have to stay on screen until they are answered, and the key handlers
// in updateList branch on them before their own key switch — a message that
// vanished on a timer while the keyboard was still held hostage by it would
// leave the user with no idea why "n" stopped opening the new-VM form. Those
// keep using m.status.
//
// ponytail: rolled here rather than taken from a library. bubbleup fits
// otherwise, but its Update returns a fresh tick for ANY message it sees
// while an alert is live, and stoat has a spinner, a cloud-init poll and the
// detail tick all in flight — measured at 21 pending ticks after 20 unrelated
// messages, i.e. ~210 full re-renders a second for as long as a toast shows.

// toastLife is how long a toast stays up. Long enough to read a VM name and
// a verb without looking for it, short enough that it is gone before it
// becomes scenery.
const toastLife = 4 * time.Second

// toastWidth is the box's content width. Errors from qemu-img and ssh are the
// long ones; they wrap inside the box rather than stretching it.
const toastWidth = 34

type toast struct {
	text string
	err  bool
	gen  int
}

// toastExpiredMsg retires the toast that gen identifies. Every new toast bumps
// the generation, so the timer belonging to a toast that has already been
// replaced dies here instead of clearing the newer one early — the same
// pattern detailGen uses for the detail screen's tick chain.
type toastExpiredMsg struct{ gen int }

// showToast puts text on screen and returns the Cmd that will take it down.
// Callers must return the Cmd, or the toast never expires.
func (m *model) showToast(text string, isErr bool) tea.Cmd {
	if text == "" {
		return nil
	}
	m.toastGen++
	m.toast = toast{text: text, err: isErr, gen: m.toastGen}
	gen := m.toastGen
	return tea.Tick(toastLife, func(time.Time) tea.Msg { return toastExpiredMsg{gen: gen} })
}

// toastStyle is the box a toast is drawn in: the same rounded border every
// pane uses, tinted by whether it is reporting a failure.
func toastStyle(isErr bool) lipgloss.Style {
	c := th.accent
	if isErr {
		c = th.err
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c).
		Foreground(c).
		Padding(0, 1).
		Width(toastWidth)
}

// renderToast draws the active toast over the top-right of an already-composed
// screen, replacing what is under it rather than pushing it aside — the screen
// keeps its exact line count and width, so nothing below the toast moves while
// it is up.
//
// This used to splice the lines by hand with the ANSI-aware cutters, because
// Lipgloss v1 had no way to put one rendered string on top of another. v2's
// compositor does it natively and does the same careful cutting internally
// (content carries styling, and cutting a styled line by byte offset slices an
// escape sequence in half), so the hand-rolled version is gone — along with
// its need to pad short lines out to the splice point by hand.
func (m model) renderToast(screen string) string {
	if m.toast.text == "" {
		return screen
	}
	box := toastStyle(m.toast.err).Render(m.toast.text)
	// One line down from the top, two cells off the right edge.
	const marginTop, marginRight = 1, 2
	x := m.width - lipgloss.Width(box) - marginRight
	if x < 0 {
		// Too narrow to inset it; sit flush left rather than let it hang off
		// the screen at a negative offset.
		x = 0
	}
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(box).X(x).Y(marginTop).Z(1),
	).Render()
}
