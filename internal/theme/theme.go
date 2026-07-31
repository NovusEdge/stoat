// Package theme holds the colors stoat names. The TUI and the installer both
// draw from here, so there is exactly one place a color is defined.
//
// The hex constants are the source of truth. The lipgloss values are derived
// from them, which is also what keeps this file portable across the Bubble Tea
// v2 migration: lipgloss.Color is a type in v1 and a function in v2, but
// `lipgloss.Color(AccentHex)` compiles and means the same thing under both, so
// only the import line changes.
package theme

import "github.com/charmbracelet/lipgloss"

const (
	AccentHex = "#C98A5B"
	UpHex     = "#7FB069"
	DownHex   = "#6C7086"
	WarnHex   = "#E0A458"
	ErrHex    = "#D16969"
	DimHex    = "#7A7A7A"
)

var (
	Accent = lipgloss.Color(AccentHex)
	Up     = lipgloss.Color(UpHex)
	Down   = lipgloss.Color(DownHex)
	Warn   = lipgloss.Color(WarnHex)
	Err    = lipgloss.Color(ErrHex)
	Dim    = lipgloss.Color(DimHex)
)
