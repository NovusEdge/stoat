package theme

import (
	"fmt"
	"math"
	"testing"
)

// darkBackground and lightBackground are the terminal backgrounds the palettes
// are measured against. They are the darkest and lightest values a common
// terminal theme uses, so a colour that clears 4.5:1 here clears it on the
// backgrounds between them.
const (
	darkBackground  = "#1E1E1E"
	lightBackground = "#F7F7F7"
)

// wcagAA is the contrast ratio WCAG 2.1 requires for body text.
const wcagAA = 4.5

func channel(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("parse %q: %v", hex, err)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func contrast(t *testing.T, fg, bg string) float64 {
	t.Helper()
	a, b := luminance(t, fg), luminance(t, bg)
	return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05)
}

func TestPalettesMeetWCAGAA(t *testing.T) {
	cases := []struct {
		name       string
		hex        string
		background string
	}{
		{"accent dark", AccentHex, darkBackground},
		{"up dark", UpHex, darkBackground},
		{"down dark", DownHex, darkBackground},
		{"warn dark", WarnHex, darkBackground},
		{"err dark", ErrHex, darkBackground},
		{"dim dark", DimHex, darkBackground},
		{"accent light", AccentLightHex, lightBackground},
		{"up light", UpLightHex, lightBackground},
		{"down light", DownLightHex, lightBackground},
		{"warn light", WarnLightHex, lightBackground},
		{"err light", ErrLightHex, lightBackground},
		{"dim light", DimLightHex, lightBackground},
	}
	for _, tc := range cases {
		got := contrast(t, tc.hex, tc.background)
		if got < wcagAA {
			t.Errorf("%s: %s on %s is %.2f:1, want at least %.1f:1", tc.name, tc.hex, tc.background, got, wcagAA)
		}
	}
}

func TestForSelectsByBackground(t *testing.T) {
	dark, light := For(true), For(false)
	if dark.Accent == light.Accent {
		t.Error("For returns the same accent for a dark and a light background")
	}
	if dark.Accent != Accent {
		t.Errorf("For(true).Accent = %v, want the package Accent %v", dark.Accent, Accent)
	}
}
