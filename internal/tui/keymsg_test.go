package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds the key message for a key named exactly as the Update
// switches name it — the string tea.KeyMsg.String() returns.
//
// Every test goes through this rather than constructing a tea.KeyMsg itself.
// The literal form, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")}, is the
// one piece of bubbletea surface in this package that changes shape rather
// than name in v2 (KeyMsg becomes an interface; KeyPressMsg carries Code and
// Text instead of Type and Runes). Funnelled here, that is one edit instead of
// nineteen spread over four files.
func keyMsg(name string) tea.KeyMsg {
	if t, ok := namedKeys[name]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// namedKeys are the keys bubbletea reports by name rather than as runes.
var namedKeys = map[string]tea.KeyType{
	"enter":     tea.KeyEnter,
	"esc":       tea.KeyEsc,
	"tab":       tea.KeyTab,
	"shift+tab": tea.KeyShiftTab,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"pgup":      tea.KeyPgUp,
	"pgdown":    tea.KeyPgDown,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
}

// TestKeyMsgRoundTrips is what makes keyMsg trustworthy: a key it builds must
// report the same name back, or a test would be pressing something other than
// what it says. It is also the guard for the v2 migration — the space bar is
// reported as "space" there rather than " ", and this fails loudly if keySpace
// and the message stop agreeing.
func TestKeyMsgRoundTrips(t *testing.T) {
	names := []string{
		"enter", "esc", "tab", "shift+tab",
		"up", "down", "left", "right", "pgup", "pgdown", "home", "end",
		keySpace,
		"i", "y", "n", "j", "k", "p", "d", "q", "s", "r", "e", "E", "G", "/", "?", "l", "h",
	}
	for _, name := range names {
		if got := keyMsg(name).String(); got != name {
			t.Errorf("keyMsg(%q).String() = %q, want %q", name, got, name)
		}
	}
}
