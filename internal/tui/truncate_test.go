package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// longMultiByte is a run of 2-byte runes, so every rune boundary sits at an
// even byte offset. Every truncation budget in this package (max 60,
// provMaxLast 34, provLabelMax 18) cuts at an odd byte index (budget - 1), so
// a byte-index slice always lands mid-rune here. ansi.Truncate cuts on rune
// and cell boundaries instead.
var longMultiByte = strings.Repeat("é", 40)

func TestTruncationKeepsValidUTF8(t *testing.T) {
	outs := map[string]string{
		"brokenReason":  brokenReason(longMultiByte),
		"provLine":      ansi.Strip(provLine(newSpinner(), "vm", provState{last: longMultiByte}, time.Now())),
		"progressLabel": ansi.Strip(progressLabel(Progress{Frac: 0.5, Label: longMultiByte}, provBarWidth)),
	}
	for name, out := range outs {
		if !utf8.ValidString(out) {
			t.Errorf("%s: output is not valid UTF-8: %q", name, out)
		}
	}
}

// TestBrokenReasonStaysWithinWidthBudget pins brokenReason's own budget,
// the one truncation site that returns the truncated string directly rather
// than wrapping it with other row content.
func TestBrokenReasonStaysWithinWidthBudget(t *testing.T) {
	const budget = 60
	out := brokenReason(longMultiByte)
	if w := lipgloss.Width(out); w > budget {
		t.Errorf("brokenReason width = %d, want <= %d: %q", w, budget, out)
	}
}
