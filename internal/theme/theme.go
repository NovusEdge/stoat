// Package theme holds the look stoat has: the colors it names, the mark it
// draws, and the one text input it builds. The TUI and the installer both draw
// from here, so each of those is defined in exactly one place. Anything two
// binaries must agree on to look like the same program belongs here; anything
// only one of them draws does not.
//
// The hex constants are the source of truth and the lipgloss values are derived
// from them, which is what carried this file across the Bubble Tea v2 migration
// unchanged: lipgloss.Color is a type in v1 and a function in v2, but
// `lipgloss.Color(AccentHex)` compiles and means the same thing under both, so
// only the import line below had to change.
package theme

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

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

// BannerArt is the mark stoat draws above both the main TUI and the installer
// transcript. At 43 columns it fits under the installer's 60-column default.
const BannerArt = `███████╗████████╗ ██████╗  █████╗ ████████╗
██╔════╝╚══██╔══╝██╔═══██╗██╔══██╗╚══██╔══╝
███████╗   ██║   ██║   ██║███████║   ██║
╚════██║   ██║   ██║   ██║██╔══██║   ██║
███████║   ██║   ╚██████╔╝██║  ██║   ██║
╚══════╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝   `

// TextInput builds a bubbles text input styled the way stoat wants every one
// of them.
//
// It exists because bubbles v2's textinput.New() hardcodes DefaultDarkStyles(),
// which paints unfocused text ANSI colour 7: on a light terminal that is grey
// on white. v1's TextStyle was a zero Style, so the value simply inherited the
// terminal's own foreground, which is right on any background and is what the
// rest of stoat relies on (this package names six colours and none of them is a
// plain foreground). Clearing Text restores that.
//
// It lives here rather than in either UI package because both build one and the
// reason above is not obvious enough to survive being fixed in only one of them.
func TextInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	st := ti.Styles()
	st.Focused.Text = lipgloss.NewStyle()
	st.Blurred.Text = lipgloss.NewStyle()
	ti.SetStyles(st)
	return ti
}
