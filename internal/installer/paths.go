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
	prefix, quote, suffix := rcLineParts(shell)
	line = prefix + quote(dir) + suffix
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc"), line
	case "bash":
		return filepath.Join(home, ".bashrc"), line
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), line
	}
	return filepath.Join(home, ".profile"), line
}

// rcLineParts is the shell syntax a line assembles from, split around the
// quoted dir itself: the text before it, the quoting function for this
// shell, and the text after. ShellRC concatenates these to build the line;
// WrapRCLine (tui.go) uses the same three pieces to break the line only
// between re-quotable chunks of dir, never inside syntax it doesn't own.
//
// dir is single-quoted rather than interpolated into surrounding double
// quotes: a bare double-quoted insertion would let a `"`, `$`, or backtick in
// dir run as shell syntax the moment the rc file is sourced, and an unquoted
// relative dir would silently make PATH depend on whatever directory the
// shell happens to be in. For the POSIX branch, the single-quoted segment
// and the double-quoted ":$PATH" that follows it concatenate into one string
// (adjacent quoted strings do that in every POSIX shell), so $PATH still
// expands while dir does not.
func rcLineParts(shell string) (prefix string, quote func(string) string, suffix string) {
	if filepath.Base(shell) == "fish" {
		return "fish_add_path ", fishQuote, ""
	}
	return `export PATH=`, shellQuote, `:"$PATH"`
}

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

// WrapRCLine breaks the rc line for shell/dir into physical lines no wider
// than width, so a renderer that clips each physical line to a terminal
// width (as Bubble Tea does) never truncates it. Pasting the result into a
// live shell reproduces the same PATH value the unbroken line would have,
// not the identical bytes. A chunk boundary that lands inside what was one
// quoted run becomes two back-to-back quoted runs instead of one longer
// one, which a shell evaluates as the same concatenated word but is not
// the same text; the value pasting it assigns is exactly dir regardless.
// See paths_test.go's TestWrapRCLineReassembles.
//
// The break itself only ever falls between two independently re-quoted
// chunks of dir: quote(s1) followed by quote(s2), concatenated with nothing
// between them, evaluates to the same word as quote(s1+s2) for both
// shellQuote and fishQuote, because both escape one input byte ("'" or "\")
// at a time, so splitting the input never splits an escape sequence, and
// chunking dir and quoting the chunks separately can never change what the
// shell assigns. Each line but the last ends with a bare "\" immediately
// before the newline: that is a shell line-continuation outside of quotes
// in every shell ShellRC targets (posix sh/bash/zsh and fish alike), so
// pasting the wrapped output into a live shell reassembles the identical
// command before it runs, unlike a bare newline, which posix shells would
// read as ending the statement, and unlike a newline *inside* the quotes,
// which would paste a literal newline into PATH.
//
// If width is too narrow to fit even one byte of a quoted chunk (plus its
// trailing "\" or the line's fixed prefix/suffix), no safe break exists and
// the whole unbroken line is returned; the caller's terminal soft-wrapping
// it is fine, unlike Bubble Tea's clip: a terminal's own wrap never
// inserts a real newline into what gets copied.
func WrapRCLine(shell, dir string, width int) []string {
	prefix, quote, suffix := rcLineParts(shell)
	full := prefix + quote(dir) + suffix
	if width < 1 || len(full) <= width {
		return []string{full}
	}

	// Every non-final line reserves as much room as the *wider* of its two
	// possible trailing markers (the continuation "\" or the real
	// suffix), not just the "\" it actually ends up using. Without that, a
	// greedy chunk sized only for "\" can swallow the entire remainder of
	// dir (since posix's suffix, `:"$PATH"`, is wider than one backslash)
	// while still failing the final-line check below, which reserves room
	// for the real suffix; the next iteration would then have nothing left
	// but "" to close out, producing a pointless trailing `'':"$PATH"` line.
	// Reserving the larger amount up front guarantees that whenever a chunk
	// would exhaust rest, the final check above already caught it first.
	reserve := len(`\`)
	if len(suffix) > reserve {
		reserve = len(suffix)
	}

	var lines []string
	rest := dir
	head := prefix
	for {
		// Try this as the final line: the whole remainder, quoted, plus the
		// real suffix instead of a continuation backslash.
		if n := len(head) + len(quote(rest)) + len(suffix); n <= width {
			return append(lines, head+quote(rest)+suffix)
		}
		// Not final: take as much of rest as fits quoted, plus reserve.
		budget := width - len(head) - reserve
		n := largestQuotableChunk(rest, quote, budget)
		if n == 0 {
			// Not even one byte of dir fits on this line at this width:
			// no safe break exists here. Bail out to the unbroken line.
			return []string{full}
		}
		lines = append(lines, head+quote(rest[:n])+`\`)
		rest = rest[n:]
		head = ""
	}
}

// largestQuotableChunk returns the largest n such that quote(s[:n]) fits
// within budget bytes. It scans byte offsets rather than rune boundaries: a
// split that lands inside a multi-byte UTF-8 rune still round-trips exactly
// once rejoined (quoting only ever inspects the ASCII quote and backslash
// bytes, never rune boundaries), so there is no correctness reason to
// prefer a rune boundary here, only a cosmetic one this rendering does not
// need to pay for.
func largestQuotableChunk(s string, quote func(string) string, budget int) int {
	lo, hi, best := 0, len(s), 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if len(quote(s[:mid])) <= budget {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
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
