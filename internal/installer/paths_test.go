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
		{"zsh", "/usr/bin/zsh", "/home/x/.zshrc", `export PATH="/home/x/.local/bin:$PATH"`},
		{"bash", "/bin/bash", "/home/x/.bashrc", `export PATH="/home/x/.local/bin:$PATH"`},
		{"fish uses its own syntax and path", "/usr/bin/fish", "/home/x/.config/fish/config.fish", "fish_add_path /home/x/.local/bin"},
		{"unknown shell falls back to profile", "/bin/ksh", "/home/x/.profile", `export PATH="/home/x/.local/bin:$PATH"`},
		{"empty SHELL falls back to profile", "", "/home/x/.profile", `export PATH="/home/x/.local/bin:$PATH"`},
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
		t.Error("second append reported added=true — the line would be duplicated")
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

// A missing rc file is normal -- a fresh account may have no .zshrc at all.
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
