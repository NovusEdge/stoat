package installer

import (
	"os"
	"path/filepath"
	"strings"
)

// OnPath reports whether dir is one of pathEnv's entries.
//
// Compared entry by entry after cleaning, never by substring: "/home/x/bin" is
// not on a PATH containing "/home/x/binx", and a substring check would say it
// was and silently skip the rc prompt.
func OnPath(dir, pathEnv string) bool {
	want := filepath.Clean(dir)
	for _, entry := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if entry == "" {
			continue
		}
		if filepath.Clean(entry) == want {
			return true
		}
	}
	return false
}

// DefaultDir is where the binary goes unless the user edits it. It mirrors the
// justfile's `prefix` so `just setup` and `just install` land in the same place.
func DefaultDir(home, prefixEnv string) string {
	if prefixEnv != "" {
		return prefixEnv
	}
	return filepath.Join(home, ".local", "bin")
}

// ShellRC picks the file to append to and the line to append, from $SHELL.
// fish is the one that needs both a different path and different syntax.
func ShellRC(shell, home, dir string) (rcPath, line string) {
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc"), exportLine(dir)
	case "bash":
		return filepath.Join(home, ".bashrc"), exportLine(dir)
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), "fish_add_path " + dir
	}
	return filepath.Join(home, ".profile"), exportLine(dir)
}

func exportLine(dir string) string { return `export PATH="` + dir + `:$PATH"` }

// AppendRC adds line to rcPath unless it is already there, reporting whether it
// wrote anything. It is the only place the installer touches a file the user
// owns, so it does the least it can: it appends, it never rewrites, and running
// it twice is the same as running it once.
func AppendRC(rcPath, line string) (added bool, err error) {
	existing, err := os.ReadFile(rcPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, l := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(l) == line {
			return false, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		return false, err
	}

	var b strings.Builder
	if len(existing) > 0 {
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		// Separate our block from whatever the file already had. A fresh file
		// has nothing to separate from, so this blank line only appears when
		// existing content precedes it.
		b.WriteString("\n")
	}
	b.WriteString("# added by the stoat installer\n")
	b.WriteString(line)
	b.WriteString("\n")

	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString(b.String()); err != nil {
		return false, err
	}
	return true, nil
}
