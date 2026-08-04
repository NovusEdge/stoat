package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// lipgloss v1.1.0's table drops the last data row when BorderBottom is off.
// fields works around that; this fails if the workaround is ever "tidied up".
func TestFieldsKeepsEveryRow(t *testing.T) {
	var f fields
	f.row("", "one", "1")
	f.row("", "two", "2")
	f.gap()
	f.row("", "three", "3")
	f.hint("a hint")

	lines := strings.Split(f.String(), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5:\n%s", len(lines), f.String())
	}
	if !strings.Contains(lines[4], "a hint") {
		t.Errorf("last row missing: %q", lines[4])
	}
}

// Every screen's value must start at the same column, whether or not the row
// carries a focus marker: that is the whole point of the shared helper.
func TestFieldsValueColumn(t *testing.T) {
	var f fields
	f.row("", "ram", "4096")
	f.row(selStyle.Render(glyphCursor), "installed", "yes")
	f.hint("indented to the value column")

	for i, line := range strings.Split(f.String(), "\n") {
		plain := stripESC(line)
		want := []string{"4096", "yes", "indented"}[i]
		// Cells, not bytes: the focus marker is a multi-byte rune.
		got := lipgloss.Width(plain[:strings.Index(plain, want)])
		if got != fieldValueColumn {
			t.Errorf("line %d: value %q at column %d, want %d (%q)",
				i, want, got, fieldValueColumn, plain)
		}
	}
}

// stripESC drops styling so a column can be measured against plain cells.
func stripESC(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
