package tui

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/theme"
)

// applyPalette must point every style at the palette for the reported
// background. The fixed dark set measured below 4.5:1 against a light
// terminal for every colour, which is what internal/theme's light set fixes.
func TestApplyPaletteFollowsTheBackground(t *testing.T) {
	t.Cleanup(func() { applyPalette(true) })

	applyPalette(false)
	light := theme.For(false)
	if th.accent != light.Accent {
		t.Errorf("accent = %v, want the light palette's %v", th.accent, light.Accent)
	}
	if got := accentStyle.GetForeground(); got != light.Accent {
		t.Errorf("accentStyle foreground = %v, want %v", got, light.Accent)
	}
	if got := paneStyle.GetBorderTopForeground(); got != light.Accent {
		t.Errorf("pane border = %v, want %v", got, light.Accent)
	}
	if got := selStyle.GetForeground(); got != light.Accent {
		t.Errorf("selStyle foreground = %v, want %v", got, light.Accent)
	}

	applyPalette(true)
	if dark := theme.For(true); th.accent != dark.Accent {
		t.Errorf("accent = %v, want the dark palette's %v", th.accent, dark.Accent)
	}
}

// Bubble Tea answers the query started in Init with this message, so Update
// has to act on it. Without the case, a light terminal keeps the dark palette
// for the life of the program.
func TestBackgroundColorMsgSwitchesThePalette(t *testing.T) {
	t.Cleanup(func() { applyPalette(true) })

	var m model
	if _, cmd := m.Update(tea.BackgroundColorMsg{Color: color.White}); cmd != nil {
		t.Errorf("the background report should not schedule work, got %T", cmd)
	}
	if light := theme.For(false); th.accent != light.Accent {
		t.Errorf("a white background left accent at %v, want %v", th.accent, light.Accent)
	}
}
