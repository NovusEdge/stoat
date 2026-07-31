package tui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// dlProgress carries byte counts from the download goroutine (iso.Download's
// callback runs there) to the UI goroutine. It is a shared cell polled on a
// ticker rather than a channel because a tea.Cmd may only ever deliver one
// message — the detail screen already tails last-provision.log the same way,
// so this is the idiom already in the codebase rather than a new one.
//
// ponytail: one global cell, not a per-download map. Exactly one download can
// be in flight — the form refuses to start a second while fetching, and
// list.go refuses to hand out a fresh form that would clear that flag — so
// there is never more than one writer. If concurrent downloads ever land, key
// this by entry ID.
//
// done and total are written in that order, so a poll landing between them
// sees the new done against a total of 0 for a single frame at the very
// start; total is constant thereafter, so ratio() never sees done > total
// from tearing. start is written before the goroutine launches.
var dlProgress struct {
	done  atomic.Int64
	total atomic.Int64 // 0 when the server sent no Content-Length
	start atomic.Int64 // unix nanos; 0 when idle
}

// dlStart resets the shared cell for a new download.
func dlStart() {
	dlProgress.done.Store(0)
	dlProgress.total.Store(0)
	dlProgress.start.Store(time.Now().UnixNano())
}

// dlRecord is the callback handed to iso.Download. A negative total means the
// response had no Content-Length; it is stored as 0 so readers have one
// "unknown" value to check instead of two.
func dlRecord(done, total int64) {
	if total < 0 {
		total = 0
	}
	dlProgress.done.Store(done)
	dlProgress.total.Store(total)
}

// dlTickMsg drives a re-render while a download is in flight.
type dlTickMsg struct{}

// dlInterval is deliberately coarser than a frame: the bar reads as smooth at
// this rate, and a download is minutes long, so there is nothing to gain from
// spending redraws on it.
const dlInterval = 200 * time.Millisecond

func dlTick() tea.Cmd {
	return tea.Tick(dlInterval, func(time.Time) tea.Msg { return dlTickMsg{} })
}

// dlStats is the snapshot the view renders: everything derived from the
// shared cell in one place, so the formatting has no clock of its own and is
// testable as a pure function.
type dlStats struct {
	done, total int64
	elapsed     time.Duration
}

func dlSnapshot(now time.Time) dlStats {
	s := dlStats{done: dlProgress.done.Load(), total: dlProgress.total.Load()}
	if start := dlProgress.start.Load(); start != 0 {
		s.elapsed = now.Sub(time.Unix(0, start))
	}
	return s
}

// ratio is the fraction complete, or -1 when the total size is unknown (no
// Content-Length) — the caller renders a byte count instead of a bar rather
// than showing a fabricated percentage.
func (s dlStats) ratio() float64 {
	if s.total <= 0 {
		return -1
	}
	if s.done >= s.total {
		return 1
	}
	return float64(s.done) / float64(s.total)
}

// speed is bytes/sec averaged over the whole download, or 0 when there is not
// yet enough of a sample to divide by. A whole-run average rather than a
// sliding window: it is steadier to read, and the only consumer is a
// human deciding whether to keep waiting.
func (s dlStats) speed() float64 {
	if s.elapsed < 500*time.Millisecond || s.done <= 0 {
		return 0
	}
	return float64(s.done) / s.elapsed.Seconds()
}

// eta is how long the rest is expected to take, or -1 when it cannot be
// derived (unknown total, or no speed sample yet).
func (s dlStats) eta() time.Duration {
	sp := s.speed()
	if s.total <= 0 || sp <= 0 || s.done >= s.total {
		return -1
	}
	// A slow first second yields an absurd estimate ("582h32m left"), and at
	// the extreme the float exceeds int64 nanoseconds, where the conversion
	// is implementation-defined. Report no estimate rather than a wrong one.
	secs := float64(s.total-s.done) / sp
	if secs > 99*3600 {
		return -1
	}
	return time.Duration(secs * float64(time.Second))
}

// line renders the text under the bar: "512 MiB / 1.2 GiB · 8.4 MiB/s · 2m10s left".
// Unknown pieces are omitted rather than shown as zeroes or a fake estimate.
func (s dlStats) line() string {
	out := humanBytes(s.done)
	if s.total > 0 {
		out += " / " + humanBytes(s.total)
	}
	if sp := s.speed(); sp > 0 {
		out += glyphSep + humanBytes(int64(sp)) + "/s"
	}
	if eta := s.eta(); eta > 0 {
		out += glyphSep + humanDuration(eta) + " left"
	}
	return out
}

// humanBytes formats a byte count in binary units. Values of 100 and up drop
// the decimal ("947 KiB", not "947.0 KiB") since the fraction is noise at
// that scale.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	format := "%.1f %ciB"
	if val >= 100 {
		format = "%.0f %ciB"
	}
	return fmt.Sprintf(format, val, "KMGT"[exp])
}

// humanDuration renders an ETA at human resolution: seconds below a minute,
// minutes and seconds below an hour, hours and minutes above. Sub-second
// precision on an estimate that is itself approximate would be false detail.
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// dlBarWidth is the bar's cell count. Fixed rather than derived from the
// terminal width: it sits inside the form pane next to fixed-width label
// columns, so a bar that grew with the window would be the only element that
// did and would look untethered.
const dlBarWidth = 32

// bar renders the filled/empty track. Built from two lipgloss-styled runs
// rather than a per-cell gradient: the accent colour already reads as "this
// is the active thing" everywhere else in the UI, and a solid fill degrades
// correctly on a terminal without truecolor.
//
// ponytail: hand-rolled instead of bubbles/progress — that component pulls in
// the harmonica module purely for spring animation, and driving the bar from
// real byte counts means ViewAs is static, so the animation would never run.
func bar(ratio float64, width int) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(width) + 0.5)
	return accentStyle.Render(strings.Repeat(glyphBarFull, filled)) +
		dimStyle.Render(strings.Repeat(glyphBarEmpty, width-filled))
}

// dlValueCol is where the form's value column starts: a 2-cell focus marker
// plus an 8-cell label plus a space (see viewForm's "%s%-8s %s" rows). The
// download block indents to the same column so it reads as another row of
// the form rather than something bolted underneath it.
const dlValueCol = "           "

// dlView renders the download block as a form row: a "download <os>" label
// line, then the bar, then the stats. When the server gave no
// Content-Length there is no bar and no percentage — a fabricated one would
// be worse than none.
func dlView(osName string, s dlStats) string {
	head := fmt.Sprintf("  %-8s %s", "download", dimStyle.Render(osName+"…"))
	body := dlValueCol + dimStyle.Render(s.line())
	r := s.ratio()
	if r < 0 {
		return head + "\n" + body
	}
	pct := dimStyle.Render(fmt.Sprintf("%3.0f%%", r*100))
	return fmt.Sprintf("%s\n%s%s %s\n%s", head, dlValueCol, bar(r, dlBarWidth), pct, body)
}
