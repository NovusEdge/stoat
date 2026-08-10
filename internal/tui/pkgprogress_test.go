package tui

import "testing"

func TestParseProgress(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Progress
	}{
		{
			"apk per-package",
			"(147/263) Installing gtk+3.0-3.24.43-r0",
			Progress{Done: 147, Total: 263, Frac: 147.0 / 263, Label: "gtk+3.0-3.24.43-r0"},
		},
		{
			"apk per-package, padded counter",
			"(  1/263) Installing dbus-libs-1.16.2-r2",
			Progress{Done: 1, Total: 263, Frac: 1.0 / 263, Label: "dbus-libs-1.16.2-r2"},
		},
		{
			"apk percent bar, hashes",
			"45% ####################",
			Progress{Frac: 0.45},
		},
		{
			"apk percent bar, block runes",
			"100% ████████████████████",
			Progress{Frac: 1},
		},
		{
			"pacman",
			"( 3/12) installing foo",
			Progress{Done: 3, Total: 12, Frac: 3.0 / 12, Label: "foo"},
		},
		{
			"dnf",
			"(3/12): pkg-1.2-3.rpm  1.2 MB/s | 3.4 MB",
			Progress{Done: 3, Total: 12, Frac: 3.0 / 12, Label: "pkg-1.2-3.rpm"},
		},
		{
			"apt fancy",
			"Progress: [ 45%]",
			Progress{Frac: 0.45},
		},
		{
			"ANSI-wrapped setup-alpine repaint",
			"\x1b7\x1b[K62% ##########################\x1b8",
			Progress{Frac: 0.62},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseProgress(c.line)
			if !ok {
				t.Fatalf("ParseProgress(%q) = false, want true", c.line)
			}
			if got != c.want {
				t.Errorf("ParseProgress(%q) = %+v, want %+v", c.line, got, c.want)
			}
		})
	}
}

func TestParseProgressNonMatches(t *testing.T) {
	nonMatches := []string{
		"",
		"   ",
		"just some plain log output",
		"Unpacking foo (1.2-3) ...",
		"Setting up foo (1.2-3) ...",
		"(15/12) installing foo",
		"(3/0) Installing foo-1.0",
		"150% ####################",
	}
	for _, line := range nonMatches {
		t.Run(line, func(t *testing.T) {
			if p, ok := ParseProgress(line); ok {
				t.Errorf("ParseProgress(%q) = %+v, true; want false", line, p)
			}
		})
	}
}
