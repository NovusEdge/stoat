package tui

import "github.com/charmbracelet/lipgloss"

// theme is the only place in the program that names a color.
type theme struct {
	accent, up, down, warn, err, dim lipgloss.Color
}

var th = theme{
	accent: lipgloss.Color("#C98A5B"),
	up:     lipgloss.Color("#7FB069"),
	down:   lipgloss.Color("#6C7086"),
	warn:   lipgloss.Color("#E0A458"),
	err:    lipgloss.Color("#D16969"),
	dim:    lipgloss.Color("#7A7A7A"),
}

var (
	accentStyle = lipgloss.NewStyle().Foreground(th.accent)
	dimStyle    = lipgloss.NewStyle().Foreground(th.dim)
	errStyle    = lipgloss.NewStyle().Foreground(th.err)
	warnStyle   = lipgloss.NewStyle().Foreground(th.warn)
	upStyle     = lipgloss.NewStyle().Foreground(th.up)
	downStyle   = lipgloss.NewStyle().Foreground(th.down)
	selStyle    = lipgloss.NewStyle().Foreground(th.accent).Bold(true)

	// paneStyle is the one border every screen draws with — a rounded box
	// in the theme accent, with breathing room inside. No screen builds its
	// own lipgloss.NewStyle().Border(...); they all go through pane().
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(th.accent).
			Padding(1, 2)

	paneTitleStyle = accentStyle.Bold(true)
)

// paneFrame is the total width a pane() call adds on top of its content:
// border on both sides plus the horizontal padding baked into paneStyle.
// Callers that need to bound a pane to the terminal width subtract this
// first.
func paneFrame() int { return paneStyle.GetHorizontalFrameSize() }

// pane draws body inside paneStyle's rounded, accent-colored box. A
// non-empty title is rendered as a bold accent heading above the body,
// inside the box, separated by a blank line. maxWidth bounds the box's
// total rendered width (border + padding included) to fit a terminal of
// that width; 0 leaves the box sized to its content.
// pane leaves the box sized to its content whenever that content already
// fits within maxWidth (0 means "always fits") — a box that hugs its rows
// reads better than one stretched to the terminal's edge. Width is only
// forced down, and only as far as maxWidth, when the content would
// otherwise overflow a narrow terminal.
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

// rowGap separates rows inside a pane. A terminal has no fractional leading,
// so "a bit more line spacing" can only mean one blank line; this names it in
// one place.
const rowGap = "\n\n"

// paneAt draws a pane whose content is held at a fixed width, for screens
// whose rows come and go — a download block, an error line, a conditional
// disk row. pane() hugs its content, so without this the box changes width
// (and re-centers) the moment an optional row appears, which reads as the
// layout jumping under the user mid-action. width is still clamped to the
// terminal, so a narrow terminal wins over the fixed width.
func paneAt(title, body string, width, maxWidth int) string {
	if maxWidth > 0 {
		if inner := maxWidth - paneFrame(); width > inner {
			width = max(inner, 1)
		}
	}
	return pane(title, lipgloss.NewStyle().Width(width).Render(body), maxWidth)
}

const bannerArt = `███████╗████████╗ ██████╗  █████╗ ████████╗
██╔════╝╚══██╔══╝██╔═══██╗██╔══██╗╚══██╔══╝
███████╗   ██║   ██║   ██║███████║   ██║
╚════██║   ██║   ██║   ██║██╔══██║   ██║
███████║   ██║   ╚██████╔╝██║  ██║   ██║
╚══════╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝   `

func banner() string { return accentStyle.Render(bannerArt) }
