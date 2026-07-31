package tui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// footerHelp renders both the short (single-line) and full (toggled by "?")
// help footers. Its Styles are intentionally blank: every color still comes
// from theme.go, applied per-binding via plainKey/styledKey below, so a
// binding like "s" (ssh) can be dimmed independently of its neighbors —
// something help.Model's own uniform Styles can't do.
var footerHelp = func() help.Model {
	h := help.New()
	blank := lipgloss.NewStyle()
	h.Styles = help.Styles{
		Ellipsis:       dimStyle,
		ShortKey:       blank,
		ShortDesc:      blank,
		ShortSeparator: dimStyle,
		FullKey:        blank,
		FullDesc:       blank,
		FullSeparator:  dimStyle,
	}
	return h
}()

// renderFooter draws the footer for km at the given terminal width, short
// form unless showAll is set (the "?" toggle). The short form is a single
// line that sits outside every pane; the full form ("?") gets its own pane,
// so it reads as a distinct help panel rather than an extension of the
// screen above it.
func renderFooter(km help.KeyMap, width int, showAll bool) string {
	h := footerHelp
	h.ShowAll = showAll
	if showAll {
		inner := width - paneFrame()
		if inner < 1 {
			inner = 1
		}
		h.Width = inner
		return pane("", h.View(km), width)
	}
	h.Width = width
	return h.View(km)
}

// styledKey builds a key.Binding whose help text is pre-rendered in style,
// so that footerHelp's blank Styles pass it through unchanged.
func styledKey(keys []string, label, desc string, style lipgloss.Style) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(style.Render(label), style.Render(desc)))
}

// plainKey is styledKey with the footer's normal (dim) color.
func plainKey(keys []string, label, desc string) key.Binding {
	return styledKey(keys, label, desc, dimStyle)
}

var keyHelp = plainKey([]string{"?"}, "?", "help")
var keyCtrlC = plainKey([]string{"ctrl+c"}, "ctrl+c", "quit")

// sshKeyStyle picks the color for the "s" (ssh) binding: normal (dim) when
// the selected VM is running and thus actually ssh-able, down (a duller,
// "offline" shade already used elsewhere for stopped/broken VMs) when it is
// not — ssh can never work in that state, so the binding stays listed
// (its position in the footer doesn't jump around) but visibly muted.
func sshKeyStyle(available bool) lipgloss.Style {
	if available {
		return dimStyle
	}
	return downStyle
}

// listHelp is the help.KeyMap for the list screen. sshAvailable reflects
// whether the currently selected row is a running (and thus ssh-able) VM.
type listHelp struct{ sshAvailable bool }

func (h listHelp) ssh() key.Binding {
	return styledKey([]string{"s"}, "s", "ssh", sshKeyStyle(h.sshAvailable))
}

func (h listHelp) ShortHelp() []key.Binding {
	return []key.Binding{
		plainKey([]string{"enter"}, "↵", "start/stop"),
		plainKey([]string{"right", "l"}, "→/l", "details"),
		h.ssh(),
		plainKey([]string{"p"}, "p", "provision"),
		plainKey([]string{"/"}, "/", "search"),
		plainKey([]string{"n"}, "n", "new"),
		plainKey([]string{"d"}, "d", "delete"),
		plainKey([]string{"q"}, "q", "quit"),
		keyHelp,
	}
}

func (h listHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{plainKey([]string{"k", "up"}, "k/↑", "up"), plainKey([]string{"j", "down"}, "j/↓", "down")},
		{plainKey([]string{"enter"}, "↵", "start/stop"), plainKey([]string{"right", "l"}, "→/l", "details")},
		{h.ssh(), plainKey([]string{"p"}, "p", "provision")},
		{plainKey([]string{"/"}, "/", "search by name"), plainKey([]string{"esc"}, "esc", "clear search")},
		{plainKey([]string{"n"}, "n", "new"), plainKey([]string{"d"}, "d", "delete")},
		{plainKey([]string{"q"}, "q", "quit"), keyCtrlC},
		{keyHelp},
	}
}

// detailHelp is the help.KeyMap for the detail screen.
type detailHelp struct{ sshAvailable bool }

func (h detailHelp) ssh() key.Binding {
	return styledKey([]string{"s"}, "s", "ssh", sshKeyStyle(h.sshAvailable))
}

func (h detailHelp) ShortHelp() []key.Binding {
	return []key.Binding{
		plainKey([]string{"e"}, "e", "edit"),
		plainKey([]string{"i"}, "i", "installed"),
		h.ssh(),
		plainKey([]string{"p"}, "p", "provision"),
		plainKey([]string{"esc", "left", "h", "q"}, "esc/←/h/q", "back"),
		keyHelp,
	}
}

func (h detailHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{plainKey([]string{"e"}, "e", "edit"), plainKey([]string{"i"}, "i", "installed")},
		{h.ssh(), plainKey([]string{"p"}, "p", "provision")},
		{plainKey([]string{"esc", "left", "h", "q"}, "esc/←/h/q", "back"), keyCtrlC},
		{keyHelp},
	}
}

// formHelp is the help.KeyMap for the new-VM form.
type formHelp struct{}

func (formHelp) ShortHelp() []key.Binding {
	return []key.Binding{
		plainKey([]string{"tab", "down"}, "tab/↓", "next"),
		plainKey([]string{"shift+tab", "up"}, "shift+tab/↑", "prev"),
		plainKey([]string{"left", "right"}, "←/→", "change"),
		plainKey([]string{" "}, "space", "download / toggle"),
		plainKey([]string{"enter"}, "↵", "create"),
		plainKey([]string{"esc"}, "esc", "cancel"),
		keyHelp,
	}
}

func (formHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{plainKey([]string{"tab", "down"}, "tab/↓", "next field"), plainKey([]string{"shift+tab", "up"}, "shift+tab/↑", "prev field")},
		{plainKey([]string{"left", "right"}, "←/→", "change iso/mode/recipe"), plainKey([]string{" "}, "space", "download image / toggle recipe")},
		{plainKey([]string{"enter"}, "↵", "create vm"), plainKey([]string{"esc"}, "esc", "cancel")},
		{keyCtrlC, keyHelp},
	}
}
