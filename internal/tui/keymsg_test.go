package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keyMsg builds the key message for a key named exactly as the Update
// switches name it: the string tea.KeyPressMsg.String() returns.
//
// Every test goes through this rather than constructing a tea.KeyPressMsg
// directly. Bubbletea v2 changed this type's shape, not just its name:
// KeyMsg became an interface, and KeyPressMsg carries Code and Text instead
// of the v1 Type and Runes fields. Funnelling every test through keyMsg
// makes a future change like that one edit here, not nineteen spread over
// four files.
func keyMsg(name string) tea.KeyPressMsg {
	if k, ok := namedKeys[name]; ok {
		return tea.KeyPressMsg(k)
	}
	r := []rune(name)
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

// namedKeys are the keys bubbletea reports by name rather than as printable
// text: anything with no Text of its own, so String() falls through to
// Keystroke() and depends on Code (and, for shift+tab, Mod).
var namedKeys = map[string]tea.Key{
	"enter":     {Code: tea.KeyEnter},
	"esc":       {Code: tea.KeyEsc},
	"tab":       {Code: tea.KeyTab},
	"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
	"up":        {Code: tea.KeyUp},
	"down":      {Code: tea.KeyDown},
	"left":      {Code: tea.KeyLeft},
	"right":     {Code: tea.KeyRight},
	"pgup":      {Code: tea.KeyPgUp},
	"pgdown":    {Code: tea.KeyPgDown},
	"home":      {Code: tea.KeyHome},
	"end":       {Code: tea.KeyEnd},
	"space":     {Code: tea.KeySpace},
}

// TestKeyMsgRoundTrips is what makes keyMsg trustworthy. A key it builds
// must report the same name back, or a test would press something other
// than what it claims. It also guards the v2 migration: v2 reports the
// space bar as "space", not " ", and this test fails loudly if keySpace and
// the message disagree.
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
