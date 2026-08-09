package tui

import (
	"regexp"
	"strconv"
	"strings"
)

// Progress is what one line of package-manager output says about an
// install in progress. Total is 0 when the source only reports a fraction,
// never invented from a count that was never printed.
type Progress struct {
	Done, Total int
	Frac        float64
	Label       string // package or action name, empty when the source names none
}

// ansiRe strips terminal escape sequences before any format match runs.
// setup-alpine redraws its progress bar in place: it wraps each repaint in
// ESC-7/ESC-8 (cursor save/restore, \x1b7 \x1b8) and clears the line with
// CSI erase-in-line (\x1b[K, \x1b[0K) first. None of that belongs to any
// manager's own line format.
var ansiRe = regexp.MustCompile(`\x1b(?:\[[0-9;]*[A-Za-z]|[78])`)

var (
	// apkCountRe matches apk's per-package line: "(147/263) Installing
	// gtk+3.0-3.24.43-r0". apk always capitalizes "Installing".
	apkCountRe = regexp.MustCompile(`^\((\d+)/(\d+)\)\s+Installing\s+(\S+)`)
	// pacmanCountRe matches pacman's per-package line: "(3/12) installing
	// foo". pacman lowercases "installing", which is what tells the two
	// apart.
	pacmanCountRe = regexp.MustCompile(`^\(\s*(\d+)/(\d+)\)\s+installing\s+(\S+)`)
	// dnfCountRe matches dnf's download line: "(3/12): pkg-1.2-3.rpm  1.2
	// MB/s | 3.4 MB". The colon after the count is what tells it apart from
	// pacman's, which has none.
	dnfCountRe = regexp.MustCompile(`^\((\d+)/(\d+)\):\s+(\S+)`)
	// percentBarRe matches apk/setup-alpine's percent bar: a line that is
	// nothing but "NN% " followed by a run of bar characters, hashes or
	// block-drawing runes.
	percentBarRe = regexp.MustCompile(`^(\d{1,3})%\s+[#=\-░▒▓█\x{2580}-\x{259F}]+\s*$`)
	// aptRe matches apt's fancy-progress line: "Progress: [ 45%]".
	aptRe = regexp.MustCompile(`^Progress:\s*\[\s*(\d{1,3})%\]$`)
)

// ParseProgress reads one line of package-manager output and reports the
// install progress it names, if any. It tries apk, pacman, and dnf's
// "(done/total)" formats first: a count carries more information than a
// bare percent, so it wins when both could apply. It falls back to
// setup-alpine's and apt's percent-only bars. Plain log lines, including
// apt's "Unpacking foo ..." and "Setting up foo ...", match none of them.
func ParseProgress(line string) (Progress, bool) {
	line = strings.TrimSpace(ansiRe.ReplaceAllString(line, ""))
	if line == "" {
		return Progress{}, false
	}

	for _, re := range []*regexp.Regexp{apkCountRe, pacmanCountRe, dnfCountRe} {
		g := re.FindStringSubmatch(line)
		if g == nil {
			continue
		}
		done, err1 := strconv.Atoi(g[1])
		total, err2 := strconv.Atoi(g[2])
		if err1 != nil || err2 != nil || total == 0 || done > total {
			return Progress{}, false
		}
		return Progress{Done: done, Total: total, Frac: float64(done) / float64(total), Label: g[3]}, true
	}

	for _, re := range []*regexp.Regexp{percentBarRe, aptRe} {
		g := re.FindStringSubmatch(line)
		if g == nil {
			continue
		}
		pct, err := strconv.Atoi(g[1])
		if err != nil || pct > 100 {
			return Progress{}, false
		}
		return Progress{Frac: float64(pct) / 100}, true
	}

	return Progress{}, false
}
