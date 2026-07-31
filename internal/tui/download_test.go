package tui

import (
	"strings"
	"testing"
	"time"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{102400, "100 KiB"}, // >= 100 drops the decimal, which is noise at that scale
		{1 << 20, "1.0 MiB"},
		{1<<30 + 1<<29, "1.5 GiB"},
		{1 << 40, "1.0 TiB"},
		{1 << 50, "1024 TiB"}, // clamps at TiB rather than inventing PiB
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{900 * time.Millisecond, "1s"},
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m30s"},
		{130 * time.Second, "2m10s"},
		{59*time.Minute + 59*time.Second, "59m59s"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Errorf("humanDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestDLStatsUnknownTotal pins the no-Content-Length behaviour: no bar, no
// percentage, no ETA. Inventing any of those from a total we don't have is
// exactly the "fake %" this feature was specified not to show.
func TestDLStatsUnknownTotal(t *testing.T) {
	s := dlStats{done: 5 << 20, total: 0, elapsed: 5 * time.Second}
	if r := s.ratio(); r != -1 {
		t.Errorf("ratio() = %v, want -1 for unknown total", r)
	}
	if e := s.eta(); e != -1 {
		t.Errorf("eta() = %v, want -1 for unknown total", e)
	}
	if s.speed() <= 0 {
		t.Error("speed() should still be known without a total")
	}

	out := dlView("ubuntu", s)
	if strings.Contains(out, "%") {
		t.Errorf("unknown total must not render a percentage: %q", out)
	}
	if strings.Contains(out, "left") {
		t.Errorf("unknown total must not render an ETA: %q", out)
	}
	if !strings.Contains(out, "MiB/s") {
		t.Errorf("speed should still show: %q", out)
	}
}

func TestDLStatsKnownTotal(t *testing.T) {
	// 50 MiB of 100 MiB in 10s => 50%, 5.0 MiB/s, 10s remaining.
	s := dlStats{done: 50 << 20, total: 100 << 20, elapsed: 10 * time.Second}

	if r := s.ratio(); r != 0.5 {
		t.Errorf("ratio() = %v, want 0.5", r)
	}
	if sp := s.speed(); sp != float64(5<<20) {
		t.Errorf("speed() = %v, want %v", sp, float64(5<<20))
	}
	if e := s.eta(); e != 10*time.Second {
		t.Errorf("eta() = %v, want 10s", e)
	}

	line := s.line()
	for _, want := range []string{"50.0 MiB", "100 MiB", "5.0 MiB/s", "10s left"} {
		if !strings.Contains(line, want) {
			t.Errorf("line() = %q, missing %q", line, want)
		}
	}
}

// TestDLStatsGuards covers the degenerate states the UI actually passes
// through: a download that has just started (no sample yet, mustn't divide by
// zero or show a wild rate) and one that has finished.
func TestDLStatsGuards(t *testing.T) {
	fresh := dlStats{done: 1024, total: 1 << 30, elapsed: 10 * time.Millisecond}
	if sp := fresh.speed(); sp != 0 {
		t.Errorf("speed() = %v, want 0 before there is a usable sample", sp)
	}
	if e := fresh.eta(); e != -1 {
		t.Errorf("eta() = %v, want -1 with no speed sample", e)
	}
	if strings.Contains(fresh.line(), "left") {
		t.Errorf("no ETA should render yet: %q", fresh.line())
	}

	done := dlStats{done: 1 << 30, total: 1 << 30, elapsed: time.Minute}
	if r := done.ratio(); r != 1 {
		t.Errorf("ratio() = %v, want 1 when complete", r)
	}
	if e := done.eta(); e != -1 {
		t.Errorf("eta() = %v, want -1 when complete", e)
	}

	// A server that over-delivers (done > total) must clamp, not exceed 100%.
	over := dlStats{done: 3 << 20, total: 1 << 20, elapsed: time.Second}
	if r := over.ratio(); r != 1 {
		t.Errorf("ratio() = %v, want clamped to 1", r)
	}
}

// TestBarWidth guards the bar's cell count against the padding bug that has
// already bitten this codebase: the runs are lipgloss-styled, so counting
// bytes instead of cells would silently produce a wrong-width bar.
func TestBarWidth(t *testing.T) {
	for _, ratio := range []float64{-1, 0, 0.25, 0.5, 1, 2} {
		got := bar(ratio, dlBarWidth)
		cells := len([]rune(stripSGR(got)))
		if cells != dlBarWidth {
			t.Errorf("bar(%v) = %d cells, want %d", ratio, cells, dlBarWidth)
		}
	}

	// Empty and full are the boundaries most likely to be off by one.
	if strings.Contains(stripSGR(bar(0, 10)), "█") {
		t.Error("bar(0) should have no filled cells")
	}
	if strings.Contains(stripSGR(bar(1, 10)), "░") {
		t.Error("bar(1) should have no empty cells")
	}
}

// stripSGR drops ANSI colour sequences so a test can count what the terminal
// actually draws.
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// TestDLRecordNormalisesUnknownTotal pins the documented contract of the one
// function that crosses goroutines: a negative Content-Length becomes 0, so
// readers have a single "unknown" value to check instead of two.
func TestDLRecordNormalisesUnknownTotal(t *testing.T) {
	dlStart()
	dlRecord(4096, -1)
	s := dlSnapshot(time.Now())
	if s.total != 0 {
		t.Errorf("total = %d, want 0 for an absent Content-Length", s.total)
	}
	if s.done != 4096 {
		t.Errorf("done = %d, want 4096", s.done)
	}
	if s.elapsed <= 0 {
		t.Error("elapsed should be positive once dlStart has run")
	}

	dlRecord(8192, 1<<20)
	if s := dlSnapshot(time.Now()); s.total != 1<<20 || s.done != 8192 {
		t.Errorf("snapshot = %+v, want done=8192 total=%d", s, int64(1<<20))
	}
}

// TestETAIsOmittedWhenAbsurd covers the guard on a slow first sample: at
// 1 KiB/s against a 2 GiB image the honest estimate is ~582 hours, and at the
// extreme the float exceeds int64 nanoseconds, where the conversion is
// implementation-defined. No estimate beats a wrong one.
func TestETAIsOmittedWhenAbsurd(t *testing.T) {
	slow := dlStats{done: 1024, total: 2 << 30, elapsed: time.Second}
	if eta := slow.eta(); eta != -1 {
		t.Errorf("eta = %v, want -1 for an absurd estimate", eta)
	}
	if strings.Contains(slow.line(), "left") {
		t.Errorf("line() should omit the ETA: %q", slow.line())
	}
	// Speed and byte counts are still real and still shown.
	if !strings.Contains(slow.line(), "/s") {
		t.Errorf("line() should still report speed: %q", slow.line())
	}

	// A pathological case that overflowed int64 nanoseconds outright.
	huge := dlStats{done: 10, total: 4 << 30, elapsed: 600 * time.Second}
	if eta := huge.eta(); eta != -1 {
		t.Errorf("eta = %v, want -1", eta)
	}
}
