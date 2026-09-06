package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/theme"
)

// th names the colors this package draws with. The values live in
// internal/theme, which the installer draws from too: a color is defined
// once, in one package, or the two binaries drift apart.
type themeColors struct {
	accent, up, down, warn, err, dim color.Color
}

var th themeColors

var (
	accentStyle lipgloss.Style
	dimStyle    lipgloss.Style
	errStyle    lipgloss.Style
	warnStyle   lipgloss.Style
	upStyle     lipgloss.Style
	downStyle   lipgloss.Style
	selStyle    lipgloss.Style

	// paneStyle is the one border every screen draws with: a rounded box
	// in the theme accent, with breathing room inside. No screen builds its
	// own lipgloss.NewStyle().Border(...); they all go through pane().
	paneStyle lipgloss.Style

	paneTitleStyle lipgloss.Style
)

// applyPalette points every style in this package at the palette for the
// terminal background the caller reports. Update calls it once, when Bubble
// Tea answers the background query started in Init.
//
// The styles stay package-level values because 150-odd call sites read them
// by name. One program draws with them at a time, and the switch happens
// before the first render that follows the query.
func applyPalette(isDark bool) {
	p := theme.For(isDark)
	th = themeColors{
		accent: p.Accent,
		up:     p.Up,
		down:   p.Down,
		warn:   p.Warn,
		err:    p.Err,
		dim:    p.Dim,
	}
	accentStyle = lipgloss.NewStyle().Foreground(th.accent)
	dimStyle = lipgloss.NewStyle().Foreground(th.dim)
	errStyle = lipgloss.NewStyle().Foreground(th.err)
	warnStyle = lipgloss.NewStyle().Foreground(th.warn)
	upStyle = lipgloss.NewStyle().Foreground(th.up)
	downStyle = lipgloss.NewStyle().Foreground(th.down)
	selStyle = lipgloss.NewStyle().Foreground(th.accent).Bold(true)
	paneStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.accent).
		Padding(1, 2)
	paneTitleStyle = accentStyle.Bold(true)
}

// A terminal that never answers the background query keeps the dark palette,
// which is what the unsuffixed constants in internal/theme are.
func init() { applyPalette(true) }

// paneFrame is the total width a pane() call adds on top of its content:
// border on both sides plus the horizontal padding baked into paneStyle. A
// caller that needs to bound a pane to the terminal width subtracts this
// first.
func paneFrame() int { return paneStyle.GetHorizontalFrameSize() }

// pane draws body inside paneStyle's rounded, accent-colored box. A
// non-empty title renders as a bold accent heading above the body, inside
// the box, separated by a blank line. maxWidth bounds the box's total
// rendered width, border and padding included, to fit a terminal of that
// width; 0 means the content always fits.
//
// The box stays sized to its content whenever the content already fits
// within maxWidth. A box that hugs its rows reads better than one
// stretched to the terminal's edge. Width is forced down only as far as
// maxWidth, and only when the content would otherwise overflow a narrow
// terminal.
func pane(title, body string, maxWidth int) string {
	content := body
	if title != "" {
		content = paneTitleStyle.Render(title) + "\n\n" + body
	}
	style := paneStyle
	if maxWidth > 0 {
		inner := maxWidth - paneFrame()
		if inner < 1 {
			inner = 1
		}
		if lipgloss.Width(content) > inner {
			style = style.Width(inner)
		}
	}
	return style.Render(content)
}

// Glyphs are named here for the same reason colours are: a symbol means one
// thing across every screen, and changing it is one edit, not a grep.
//
// There is nothing upstream to reuse. bubbletea and lipgloss export no
// glyphs at all, and bubbles keeps its own bullet unexported and writes
// arrows as raw literals in its key help. These are ours to name.
const (
	glyphCursor   = "❯ " // the focused row / selected item marker
	glyphRadioOn  = "(•)"
	glyphRadioOff = "( )"
	glyphWas      = "←" // points at the value a field used to hold
	glyphSep      = " · "
	glyphDownload = "⤓"
	glyphBarFull  = "█"
	glyphBarEmpty = "░"
	glyphRunning  = "●"
	glyphStopped  = "○"
	glyphBroken   = "✗"
	glyphTo       = "→" // "share → /mnt/host", "8G → 16G"
)

// radio renders one option of an inline radio row. bubbles has no select
// component (its list is a whole screen, not a three-way inline toggle), so
// this is the smallest thing that keeps every radio in the UI identical.
func radio(label string, on bool) string {
	if on {
		return glyphRadioOn + " " + label
	}
	return glyphRadioOff + " " + label
}

// appContentWidth is the left edge every screen's stacked panes share. Before
// this, each pane centered on its own width, so switching screens, or a pane
// changing size as content appeared, shifted the whole block sideways.
const appContentWidth = 72

// column stacks parts left-aligned inside one block, at least width cells
// wide. Every part then starts at the same left edge, instead of each one
// centering on its own width.
//
// The block widens for a part wider than width rather than wrapping it: a
// lipgloss Style.Width() narrower than its content word-wraps that content,
// which mangles a bordered pane's box-drawing runs instead of just leaving
// it be. The list pane plus its side-by-side access box is routinely wider
// than appContentWidth, so this has to hold.
func column(width int, parts ...string) string {
	for _, p := range parts {
		if w := lipgloss.Width(p); w > width {
			width = w
		}
	}
	return lipgloss.NewStyle().Width(width).Align(lipgloss.Left).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// paneAt draws a pane whose content is held at a fixed width, for screens
// whose rows come and go, e.g. a download block, an error line, a
// conditional disk row. pane() hugs its content, so without this the box
// changes width, and re-centers, the moment an optional row appears, which
// reads as the layout jumping under the user mid-action. width is still
// clamped to the terminal, so a narrow terminal wins over the fixed width.
func paneAt(title, body string, width, maxWidth int) string {
	if maxWidth > 0 {
		if inner := maxWidth - paneFrame(); width > inner {
			width = max(inner, 1)
		}
	}
	return pane(title, lipgloss.NewStyle().Width(width).Render(body), maxWidth)
}

func banner() string { return accentStyle.Render(theme.BannerArt) }
