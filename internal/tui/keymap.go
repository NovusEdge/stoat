package tui

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// keySpace is what tea.KeyMsg.String() returns for the space bar, named once
// because it is the one binding in the program whose spelling is decided by
// bubbletea rather than by us — v2 reports it as "space" rather than " ".
// Every switch case, key.Binding and test that means "space" goes through
// this, so that change is one edit and not a silent no-match at runtime.
const keySpace = "space"

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
		h.SetWidth(inner)
		return pane("", h.View(km), width)
	}
	h.SetWidth(width)
	// help.Model gives up truncating once its running total passes the width
	// (it can only cut where an ellipsis still fits), and then appends every
	// remaining binding — so the short footer can come back WIDER than the
	// terminal and wrap, pushing the whole screen up. Cut it here instead.
	return ansi.Truncate(h.View(km), width, "…")
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
		plainKey([]string{"r"}, "r", "recipes"),
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
		{plainKey([]string{"r"}, "r", "edit recipes in $EDITOR")},
		{plainKey([]string{"q"}, "q", "quit"), keyCtrlC},
		{keyHelp},
	}
}

// detailHelp is the help.KeyMap for the detail screen. consolePassword
// reflects whether the selected VM is running and has a console password set
// — the "t" (type into guest) and "c" (copy to host clipboard) bindings only
// do anything then, so they are only listed then.
type detailHelp struct {
	sshAvailable    bool
	consolePassword bool
}

func (h detailHelp) ssh() key.Binding {
	return styledKey([]string{"s"}, "s", "ssh", sshKeyStyle(h.sshAvailable))
}

func (h detailHelp) ShortHelp() []key.Binding {
	keys := []key.Binding{
		plainKey([]string{"e"}, "e", "edit"),
		plainKey([]string{"E"}, "E", "raw toml"),
		plainKey([]string{"i"}, "i", "installed"),
		h.ssh(),
		plainKey([]string{"p"}, "p", "provision"),
	}
	if h.consolePassword {
		keys = append(keys, plainKey([]string{"t"}, "t", "type password"))
	}
	return append(keys,
		plainKey([]string{"esc", "left", "h", "q"}, "esc/←/h/q", "back"),
		keyHelp,
	)
}

func (h detailHelp) FullHelp() [][]key.Binding {
	rows := [][]key.Binding{
		{plainKey([]string{"e"}, "e", "edit form"), plainKey([]string{"E"}, "E", "raw vm.toml in $EDITOR")},
		{plainKey([]string{"i"}, "i", "installed"), h.ssh()},
		{plainKey([]string{"p"}, "p", "provision")},
	}
	if h.consolePassword {
		rows = append(rows, []key.Binding{plainKey([]string{"t"}, "t", "type console password into guest")})
	}
	return append(rows,
		[]key.Binding{plainKey([]string{"esc", "left", "h", "q"}, "esc/←/h/q", "back"), keyCtrlC},
		[]key.Binding{keyHelp},
	)
}

// formHelp is the help.KeyMap for the new-VM form.
type formHelp struct{}

func (formHelp) ShortHelp() []key.Binding {
	return []key.Binding{
		plainKey([]string{"tab", "down"}, "tab/↓", "next"),
		plainKey([]string{"shift+tab", "up"}, "shift+tab/↑", "prev"),
		plainKey([]string{"left", "right"}, "←/→", "change"),
		plainKey([]string{keySpace}, "space", "download / toggle"),
		plainKey([]string{"enter"}, "↵", "create"),
		plainKey([]string{"esc"}, "esc", "cancel"),
		keyHelp,
	}
}

func (formHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{plainKey([]string{"tab", "down"}, "tab/↓", "next field"), plainKey([]string{"shift+tab", "up"}, "shift+tab/↑", "prev field")},
		{plainKey([]string{"left", "right"}, "←/→", "pick image · change mode/recipe"), plainKey([]string{keySpace}, "space", "download image / toggle recipe")},
		{plainKey([]string{"enter"}, "↵", "create vm"), plainKey([]string{"esc"}, "esc", "cancel")},
		{keyCtrlC, keyHelp},
	}
}

// editHelp is the help.KeyMap for the in-TUI edit form.
type editHelp struct{}

func (editHelp) ShortHelp() []key.Binding {
	return []key.Binding{
		plainKey([]string{"tab", "down"}, "tab/↓", "next"),
		plainKey([]string{"left", "right"}, "←/→", "change"),
		plainKey([]string{keySpace}, "space", "toggle recipe"),
		plainKey([]string{"enter"}, "↵", "save"),
		plainKey([]string{"esc"}, "esc", "cancel"),
		keyHelp,
	}
}

func (editHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{plainKey([]string{"tab", "down"}, "tab/↓", "next field"), plainKey([]string{"shift+tab", "up"}, "shift+tab/↑", "prev field")},
		{plainKey([]string{"left", "right"}, "←/→", "change mode/recipe"), plainKey([]string{keySpace}, "space", "toggle recipe")},
		{plainKey([]string{"enter"}, "↵", "save"), plainKey([]string{"esc"}, "esc", "cancel without saving")},
		{keyCtrlC, keyHelp},
	}
}
