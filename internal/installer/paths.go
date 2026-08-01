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
		return filepath.Join(home, ".config", "fish", "config.fish"), "fish_add_path " + fishQuote(dir)
	}
	return filepath.Join(home, ".profile"), exportLine(dir)
}

// exportLine is a POSIX sh/bash/zsh export. dir is single-quoted rather than
// interpolated into the surrounding double quotes: a bare double-quoted
// insertion would let a `"`, `$`, or backtick in dir run as shell syntax the
// moment the rc file is sourced, and an unquoted relative dir would silently
// make PATH depend on whatever directory the shell happens to be in. The
// single-quoted segment and the double-quoted ":$PATH" that follows it
// concatenate into one string -- adjacent quoted strings do that in every
// POSIX shell -- so $PATH still expands while dir does not.
func exportLine(dir string) string { return `export PATH=` + shellQuote(dir) + `:"$PATH"` }

// shellQuote wraps s in single quotes for a POSIX shell. Inside single quotes
// nothing is special except the quote character itself, so the only escape
// needed is the standard one: close the quote, emit an escaped literal quote,
// reopen it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fishQuote wraps s in single quotes for fish. Fish's single-quote escaping
// differs from POSIX's: backslash is special there too (it is how you get a
// literal backslash or a literal quote), so both have to be escaped, in that
// order so an escaped quote's own backslash is not re-escaped.
func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

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
	// Close is checked rather than deferred and ignored, same as build.go's
	// Install: this is the one function that writes to a file the user owns,
	// so a close-time failure (a full disk, most plausibly) must not be
	// reported as added=true.
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	return true, nil
}
