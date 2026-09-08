// Package ui writes stoat's CLI prose. It owns the one decision every
// command used to make for itself: whether this line is suppressed by
// --quiet, and whether ANSI colour is safe on this stream.
//
// The colours come from internal/theme, so the CLI, the TUI and the
// installer name the same six. A CLI cannot ask the terminal for its
// background the way a Bubble Tea program can, so this package takes the
// dark palette, which is what theme's unsuffixed constants assume.
package ui

import (
	"fmt"
	"image/color"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/novusedge/stoat/internal/theme"
)

// Writer renders one command's prose to a single stream.
type Writer struct {
	out   io.Writer
	quiet bool
	color bool
	p     theme.Palette
}

// New builds a Writer for out. quiet silences the progress shapes (Step and
// Hint) and leaves the outcome shapes (Done, Warn, Fail) alone: a caller
// that passes --quiet wants less narration, not a silent failure.
//
// Colour follows out itself, so `stoat ls | awk ...` never carries escape
// codes even when stderr is still a terminal.
func New(out io.Writer, quiet bool) *Writer {
	return &Writer{out: out, quiet: quiet, color: colorOK(out), p: theme.For(true)}
}

// colorOK reports whether ANSI colour may go to w.
func colorOK(w io.Writer) bool {
	if !colorAllowedByEnv() {
		return false
	}
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// colorAllowedByEnv reads the two environment vetoes. NO_COLOR counts at any
// value including the empty string, per the no-color.org convention, and
// TERM=dumb marks a terminal that echoes escape codes without rendering them.
func colorAllowedByEnv() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	return os.Getenv("TERM") != "dumb"
}

func (w *Writer) paint(c color.Color, s string) string {
	if !w.color {
		return s
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

// Step reports work about to start. --quiet drops it.
func (w *Writer) Step(format string, args ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintf(w.out, format+"\n", args...)
}

// Done reports work that finished. It stays indented under the Step that
// announced it.
func (w *Writer) Done(format string, args ...any) {
	fmt.Fprintf(w.out, "  "+format+"\n", args...)
}

// Warn reports a condition the caller should know about, where the command
// still succeeded.
func (w *Writer) Warn(format string, args ...any) {
	fmt.Fprintf(w.out, "%s %s\n", w.paint(w.p.Warn, "!"), fmt.Sprintf(format, args...))
}

// Fail reports why a command is giving up.
func (w *Writer) Fail(format string, args ...any) {
	fmt.Fprintf(w.out, "%s %s\n", w.paint(w.p.Err, "x"), fmt.Sprintf(format, args...))
}

// Hint offers a next command to run. --quiet drops it.
func (w *Writer) Hint(format string, args ...any) {
	if w.quiet {
		return
	}
	fmt.Fprintln(w.out, w.paint(w.p.Dim, fmt.Sprintf(format, args...)))
}

// Plain writes a line with no shape and no colour. Machine-read output —
// a path, an ssh argv, one row of a listing — goes through here, so a
// caller never has to reach past the Writer to reach its own stream.
func (w *Writer) Plain(format string, args ...any) {
	fmt.Fprintf(w.out, format+"\n", args...)
}

// State pads a VM state to width and then colours it. The order matters:
// escape codes are zero-width on screen but count toward %-*s, so colouring
// first eats nine columns and skews every row after the STATE column.
func (w *Writer) State(state string, width int) string {
	padded := fmt.Sprintf("%-*s", width, state)
	switch state {
	case "running":
		return w.paint(w.p.Up, padded)
	case "broken":
		return w.paint(w.p.Err, padded)
	case "stopped":
		return w.paint(w.p.Down, padded)
	}
	return padded
}
