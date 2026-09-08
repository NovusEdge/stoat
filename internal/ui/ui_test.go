package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/theme"
)

// A bytes.Buffer is not an *os.File, so New leaves colour off and these
// tests assert plain text. TestColorOKRejects covers the gating itself.
func TestQuietDropsProgressAndKeepsOutcomes(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, true)
	w.Step("starting dev...")
	w.Hint("ssh with: stoat ssh dev")
	w.Done("dev started")
	w.Warn("no console password")
	w.Fail("apply failed")

	got := out.String()
	for _, absent := range []string{"starting dev", "ssh with"} {
		if strings.Contains(got, absent) {
			t.Errorf("--quiet kept %q:\n%s", absent, got)
		}
	}
	for _, present := range []string{"dev started", "no console password", "apply failed"} {
		if !strings.Contains(got, present) {
			t.Errorf("--quiet dropped %q:\n%s", present, got)
		}
	}
}

func TestShapes(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, false)
	w.Step("starting %s...", "dev")
	w.Done("%s started (ssh :%d)", "dev", 2222)
	w.Warn("console password not set")
	w.Fail("apply failed: recipe %s", "python-dev")

	want := "starting dev...\n  dev started (ssh :2222)\n! console password not set\nx apply failed: recipe python-dev\n"
	if got := out.String(); got != want {
		t.Errorf("output\n%q\nwant\n%q", got, want)
	}
}

func TestStatePadsBeforeColouring(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, false)
	if got := w.State("running", 8); got != "running " {
		t.Errorf("State(running, 8) = %q, want %q", got, "running ")
	}
	if got := w.State("stopped", 3); got != "stopped" {
		t.Errorf("State truncated a state longer than width: %q", got)
	}
}

// TestStateWidthHoldsUnderColour pins the pad-then-colour order. The escape
// sequences are zero-width on screen but count toward a %-*s verb, so
// colouring first under-pads every coloured row and every field after STATE
// drifts left. The Writer is built by hand because colorOK refuses a
// non-terminal, which is every test process.
func TestStateWidthHoldsUnderColour(t *testing.T) {
	w := &Writer{out: &bytes.Buffer{}, color: true, p: theme.For(true)}
	for _, state := range []string{"running", "stopped", "broken"} {
		got := w.State(state, 8)
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("State(%q) emitted no escape with colour on: %q", state, got)
		}
		if visible := stripANSI(got); len(visible) != 8 {
			t.Errorf("State(%q, 8) visible width = %d (%q), want 8", state, len(visible), visible)
		}
	}
}

// stripANSI drops CSI colour sequences so a test can measure what the
// terminal actually shows.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestColorOKRejectsNonTerminal(t *testing.T) {
	var out bytes.Buffer
	if colorOK(&out) {
		t.Error("colorOK said yes for a bytes.Buffer")
	}
}

func TestEnvVetoes(t *testing.T) {
	t.Run("NO_COLOR empty still vetoes", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		if colorAllowedByEnv() {
			t.Error("NO_COLOR set to the empty string did not veto colour")
		}
	})
	t.Run("TERM=dumb vetoes", func(t *testing.T) {
		t.Setenv("TERM", "dumb")
		if colorAllowedByEnv() {
			t.Error("TERM=dumb did not veto colour")
		}
	})
	t.Run("a normal terminal is allowed", func(t *testing.T) {
		t.Setenv("TERM", "xterm-256color")
		if !colorAllowedByEnv() {
			t.Error("TERM=xterm-256color was vetoed")
		}
	})
}
