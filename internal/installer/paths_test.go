package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnPath(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		pathEnv string
		want    bool
	}{
		{"present", "/home/x/.local/bin", "/usr/bin:/home/x/.local/bin", true},
		{"absent", "/home/x/.local/bin", "/usr/bin:/bin", false},
		{"only entry", "/home/x/.local/bin", "/home/x/.local/bin", true},
		{"trailing separator", "/home/x/.local/bin", "/usr/bin:/home/x/.local/bin:", true},
		{"empty entries ignored", "/home/x/.local/bin", "::/home/x/.local/bin", true},
		{"trailing slash still matches", "/home/x/.local/bin", "/home/x/.local/bin/", true},
		{"dir given with trailing slash", "/home/x/.local/bin/", "/home/x/.local/bin", true},
		// The one that a naive strings.Contains gets wrong.
		{"prefix is not a match", "/home/x/.local/bin", "/home/x/.local/binx", false},
		{"suffix is not a match", "/home/x/bin", "/home/x/local-bin", false},
		{"empty PATH", "/home/x/.local/bin", "", false},
		{"unclean entry is normalised", "/home/x/.local/bin", "/home/x/.local/../.local/bin", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OnPath(tt.dir, tt.pathEnv); got != tt.want {
				t.Errorf("OnPath(%q, %q) = %v, want %v", tt.dir, tt.pathEnv, got, tt.want)
			}
		})
	}
}

func TestDefaultDir(t *testing.T) {
	if got, want := DefaultDir("/home/x", ""), "/home/x/.local/bin"; got != want {
		t.Errorf("DefaultDir with no PREFIX = %q, want %q", got, want)
	}
	if got, want := DefaultDir("/home/x", "/opt/bin"), "/opt/bin"; got != want {
		t.Errorf("DefaultDir with PREFIX = %q, want %q", got, want)
	}
}

func TestShellRC(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		wantRC   string
		wantLine string
	}{
		{"zsh", "/usr/bin/zsh", "/home/x/.zshrc", `export PATH='/home/x/.local/bin':"$PATH"`},
		{"bash", "/bin/bash", "/home/x/.bashrc", `export PATH='/home/x/.local/bin':"$PATH"`},
		{"fish uses its own syntax and path", "/usr/bin/fish", "/home/x/.config/fish/config.fish", "fish_add_path '/home/x/.local/bin'"},
		{"unknown shell falls back to profile", "/bin/ksh", "/home/x/.profile", `export PATH='/home/x/.local/bin':"$PATH"`},
		{"empty SHELL falls back to profile", "", "/home/x/.profile", `export PATH='/home/x/.local/bin':"$PATH"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc, line := ShellRC(tt.shell, "/home/x", "/home/x/.local/bin")
			if rc != tt.wantRC {
				t.Errorf("rc = %q, want %q", rc, tt.wantRC)
			}
			if line != tt.wantLine {
				t.Errorf("line = %q, want %q", line, tt.wantLine)
			}
		})
	}
}

// The behaviour that matters: running the installer twice must not leave two
// export lines in the rc file.
func TestAppendRCIsIdempotent(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".zshrc")
	original := "# my zshrc\nalias ll='ls -l'\n"
	if err := os.WriteFile(rc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	line := `export PATH="/home/x/.local/bin:$PATH"`

	added, err := AppendRC(rc, line)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("first append reported added=false")
	}

	added, err = AppendRC(rc, line)
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("second append reported added=true: the line would be duplicated")
	}

	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), line); n != 1 {
		t.Errorf("line appears %d times, want exactly 1:\n%s", n, got)
	}
	if !strings.HasPrefix(string(got), original) {
		t.Errorf("the original content was not preserved verbatim:\n%s", got)
	}
}

// A missing rc file is normal: a fresh account may have no .zshrc at all.
func TestAppendRCCreatesMissingFile(t *testing.T) {
	rc := filepath.Join(t.TempDir(), "nested", ".zshrc")
	line := `export PATH="/home/x/.local/bin:$PATH"`

	added, err := AppendRC(rc, line)
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Error("added = false when creating a new file")
	}
	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), line) {
		t.Errorf("new file does not contain the line:\n%s", got)
	}
	if strings.HasPrefix(string(got), "\n") {
		t.Errorf("a freshly created file must not start with a blank line:\n%q", got)
	}
}

// A file not ending in a newline must not get the export glued onto its last line.
func TestAppendRCHandlesMissingTrailingNewline(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rc, []byte("alias ll='ls -l'"), 0o644); err != nil {
		t.Fatal(err)
	}
	line := `export PATH="/home/x/.local/bin:$PATH"`

	if _, err := AppendRC(rc, line); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "ls -l'export") {
		t.Errorf("the export was glued onto the previous line:\n%s", got)
	}
	for _, l := range strings.Split(string(got), "\n") {
		if l == line {
			return
		}
	}
	t.Errorf("the export is not on a line of its own:\n%s", got)
}

// unquotePosixRun reverses a posix "word" built only from adjacent
// single-quoted runs and backslash-escaped single characters outside
// quotes. shellQuote produces exactly these two constructs, whether called
// once on the whole dir or once per chunk.
//
// This function follows posix quoting rules directly, not shellQuote's
// logic. It gives an independent check of what a real shell assigns, not a
// tautology against the code under test.
//
// It stops at the first byte that starts neither construct, and returns
// that remainder unconsumed.
func unquotePosixRun(s string) (value, rest string) {
	var b strings.Builder
	for len(s) > 0 {
		switch {
		case s[0] == '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end == -1 {
				return b.String(), s
			}
			b.WriteString(s[1 : 1+end])
			s = s[1+end+1:]
		case s[0] == '\\' && len(s) > 1:
			b.WriteByte(s[1])
			s = s[2:]
		default:
			return b.String(), s
		}
	}
	return b.String(), s
}

// unquoteFishRun reverses a fish word made of adjacent single-quoted runs,
// what fishQuote produces per chunk. It unescapes fish's own `\\` and `\'`
// within each run. It is independent of fishQuote for the same reason
// unquotePosixRun is independent of shellQuote.
func unquoteFishRun(s string) (value, rest string) {
	var b strings.Builder
outer:
	for len(s) > 0 && s[0] == '\'' {
		s = s[1:]
		for {
			switch {
			case len(s) == 0:
				break outer // malformed input; stop rather than loop forever
			case strings.HasPrefix(s, `\\`):
				b.WriteByte('\\')
				s = s[2:]
			case strings.HasPrefix(s, `\'`):
				b.WriteByte('\'')
				s = s[2:]
			case s[0] == '\'':
				s = s[1:]
				continue outer
			default:
				b.WriteByte(s[0])
				s = s[1:]
			}
		}
	}
	return b.String(), s
}

// pasteValue simulates what a real shell assigns after a WrapRCLine result
// is pasted. It deletes each line's trailing "\" continuation marker: a real
// paste carries the newline that marker precedes, and a shell deletes a
// backslash-newline pair outright before tokenizing. It concatenates the
// lines with no separator, confirms the fixed prefix and suffix survived
// untouched, and unquotes whatever sits between them.
//
// This is the ground truth WrapRCLine's paste-safety claim rests on.
func pasteValue(t *testing.T, shell string, wrapped []string) string {
	t.Helper()
	var joined strings.Builder
	for _, l := range wrapped {
		joined.WriteString(strings.TrimSuffix(l, `\`))
	}
	line := joined.String()

	prefix, _, suffix := rcLineParts(shell)
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok {
		t.Fatalf("reassembled line %q does not start with %q", line, prefix)
	}

	var value string
	if filepath.Base(shell) == "fish" {
		value, rest = unquoteFishRun(rest)
	} else {
		value, rest = unquotePosixRun(rest)
	}
	if rest != suffix {
		t.Fatalf("reassembled line %q: after unquoting the value, %q is left over, want the suffix %q", line, rest, suffix)
	}
	return value
}

// However WrapRCLine breaks a line, pasting the result into a real shell
// must assign the exact same PATH value as ShellRC's unbroken line. When it
// does break the line into more than one physical line, every line must fit
// the width it was asked for. Otherwise wrapping defeats its own purpose of
// avoiding Bubble Tea's per-line clip.
//
// This covers both shell syntaxes ShellRC emits. It covers directories
// containing the characters each syntax's quoting escapes: a single quote
// for posix, a backslash and a single quote for fish. These characters can
// land at any point a chunk boundary could fall.
func TestWrapRCLineReassembles(t *testing.T) {
	tests := []struct {
		name  string
		shell string
		dir   string
	}{
		{"posix", "/usr/bin/zsh", "/home/exampleuser/" + strings.Repeat("nested/", 10) + "bin"},
		{"posix with a quote", "/usr/bin/bash", `/home/x/my dir's stuff/` + strings.Repeat("pad-", 15)},
		{"fish", "/usr/bin/fish", "/home/exampleuser/" + strings.Repeat("nested/", 10) + "bin"},
		{"fish with backslash and quote", "/usr/bin/fish", `/home/x/odd\dir'name/` + strings.Repeat("pad-", 15)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, width := range []int{15, 20, 30, 60, 200} {
				lines := WrapRCLine(tt.shell, tt.dir, width)
				if len(lines) > 1 {
					for _, l := range lines {
						if len(l) > width {
							t.Errorf("width=%d: line %q is %d chars, wider than the terminal", width, l, len(l))
						}
					}
				}
				if got := pasteValue(t, tt.shell, lines); got != tt.dir {
					t.Errorf("width=%d: pasting %v assigns %q, want %q", width, lines, got, tt.dir)
				}
			}
		})
	}
}

// A width too narrow to fit even a single quoted byte has no safe break.
// WrapRCLine must hand back the line unbroken rather than corrupt it. A
// terminal's own soft-wrap of that line is fine: unlike Bubble Tea's clip,
// it never inserts a real newline into what gets pasted.
func TestWrapRCLineFallsBackWhenNoSafeBreakFits(t *testing.T) {
	_, want := ShellRC("/usr/bin/zsh", "/home/x", "/home/x/.local/bin")
	got := WrapRCLine("/usr/bin/zsh", "/home/x/.local/bin", 1)
	if len(got) != 1 || got[0] != want {
		t.Errorf("WrapRCLine at width=1 = %q, want the unbroken line %q", got, want)
	}
}

// A directory with a space and a quote breaks a naive implementation. The
// space would split fish_add_path's argument in two. The quote would end
// the POSIX export's string early. Both branches must quote it correctly
// enough to round-trip.
func TestShellRCQuotesSpaceAndQuote(t *testing.T) {
	dir := `/home/x/my dir's stuff`

	_, posix := ShellRC("/usr/bin/zsh", "/home/x", dir)
	if want := `export PATH='/home/x/my dir'\''s stuff':"$PATH"`; posix != want {
		t.Errorf("posix line = %q, want %q", posix, want)
	}

	_, fish := ShellRC("/usr/bin/fish", "/home/x", dir)
	if want := `fish_add_path '/home/x/my dir\'s stuff'`; fish != want {
		t.Errorf("fish line = %q, want %q", fish, want)
	}
}
