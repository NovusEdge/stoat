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
)

const bannerArt = `
███████╗████████╗ ██████╗  █████╗ ████████╗
██╔════╝╚══██╔══╝██╔═══██╗██╔══██╗╚══██╔══╝
███████╗   ██║   ██║   ██║███████║   ██║
╚════██║   ██║   ██║   ██║██╔══██║   ██║
███████║   ██║   ╚██████╔╝██║  ██║   ██║
╚══════╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝   `

func banner() string { return accentStyle.Render(bannerArt) }
