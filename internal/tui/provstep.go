package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// provState is what the UI knows about one in-flight provision run. The UI
// shows which recipe is running, its last printed line, how long the run has
// taken, and a package-install bar whenever the underlying package manager's
// own output names one. hasProg is false, and no bar shown, whenever it
// doesn't: a made-up percentage is worse than none.
type provState struct {
	start   time.Time
	step    string // recipe name, or the phase before one starts
	last    string // the log's most recent output line
	prog    Progress
	hasProg bool
}

// provTailBytes is how much of the end of last-provision.log gets read to
// find the current step. A desktop install writes hundreds of KB. Re-reading
// all of it several times a second to find the last line wastes work, and
// the tail is all that "current" needs.
const provTailBytes = 8 << 10

// tailBytes returns up to n bytes from the end of path.
func tailBytes(path string, n int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	if fi.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return nil
		}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return b
}

// readProvStep derives the current step from the tail of a VM's provision log.
// Parsing the log the run already produces avoids plumbing progress through a
// channel, and the CLI's `stoat provision` reads the same file the same way.
// Returns the step (which recipe, or the phase before one starts), the most
// recent line of real output, and the newest package-progress line the tail
// contains, if any.
func readProvStep(v core.VM) (step, last string, prog Progress, hasProg bool) {
	b := tailBytes(v.Paths.ApplyLog, provTailBytes)
	if len(b) == 0 {
		return "starting", "", Progress{}, false
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		name, isMarker := sshx.ParseRecipeMarker(l)
		if step == "" && isMarker {
			step = name
		}
		if last == "" && !isMarker {
			last = l
		}
		if step != "" && last != "" {
			break
		}
	}

	if step == "" {
		// No recipe has started yet: the only thing written so far is
		// sshx.Provision's "waiting for ssh on port N…".
		step = "waiting for ssh"
		if last != "" && strings.HasPrefix(last, "waiting for ssh") {
			last = ""
		}
	}

	for i := len(lines) - 1; i >= 0; i-- {
		if p, ok := ParseProgress(lines[i]); ok {
			return step, last, p, true
		}
	}
	return step, last, Progress{}, false
}

// provElapsed renders a duration at the resolution a human watching a
// multi-minute install actually reads.
func provElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// provMaxLast caps the echoed log line so a long apk/apt line can't widen the
// pane or wrap it.
const provMaxLast = 34

// provLine renders one in-flight run: spinner, VM, current step, elapsed,
// and either a package-progress bar or the log's last output line, whichever
// the tail has to offer. The bar replaces the last-output text instead of
// sitting beside it: both describe the same log line, and the row has no
// room to say it twice.
func provLine(spin spinner.Model, name string, st provState, now time.Time) string {
	out := spin.View() + " " + accentStyle.Render(name) + dimStyle.Render(" · "+st.step)
	switch {
	case st.hasProg:
		out += " " + progressLabel(st.prog, provBarWidth)
	case st.last != "":
		out += dimStyle.Render(" · " + ansi.Truncate(st.last, provMaxLast, "…"))
	}
	out += dimStyle.Render(" · " + provElapsed(now.Sub(st.start)))
	// A long VM name can still push the line past the pane and wrap what
	// should be one row. Every field above already has its own cap; this
	// is the backstop for the sum of them.
	return ansi.Truncate(out, appContentWidth, "…")
}

// installLine renders one disk VM mid unattended install the same way
// provLine renders a recipe run: spinner, VM, phase, bar, elapsed. There is
// no step name to show (installLine has no log line to derive one from, only
// the console transcript), so the phase is always "installing".
func installLine(spin spinner.Model, v core.VM, now time.Time) string {
	out := spin.View() + " " + accentStyle.Render(v.Name) + dimStyle.Render(" · installing")
	if p, ok := latestInstallProgress(v.Name); ok {
		out += " " + progressLabel(p, provBarWidth)
	}
	elapsed := time.Duration(0)
	if !v.StartedAt.IsZero() {
		elapsed = now.Sub(v.StartedAt)
	}
	return out + dimStyle.Render(" · "+provElapsed(elapsed))
}

// latestInstallProgress reads the tail of a disk VM's console log and
// returns the newest line ParseProgress can read as install progress. The
// console log is the only record of an unattended install: it runs before
// ssh exists, so there is no apply log yet to read instead.
func latestInstallProgress(name string) (Progress, bool) {
	rc, err := core.Logs(name, core.WhichConsole)
	if err != nil {
		return Progress{}, false
	}
	content, _, err := tailReadCloser(rc, provTailBytes)
	if err != nil {
		return Progress{}, false
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if p, ok := ParseProgress(lines[i]); ok {
			return p, true
		}
	}
	return Progress{}, false
}

// anyInstalling reports whether vms contains a disk VM running its own
// unattended install: the same test provLines uses to decide whether to draw
// an install-phase line. app.go's spinner.TickMsg case uses it too, to
// re-arm the tick chain for a VM that entered this state without a "p"
// press ever landing in m.provisioning.
func anyInstalling(vms []core.VM) bool {
	for _, v := range vms {
		if v.Mode == "disk" && !v.Installed && v.State == core.StateRunning {
			return true
		}
	}
	return false
}

// provLines renders every in-flight run, newest state first read fresh from
// the logs: package installs on uninstalled disk VMs booting for the first
// time, then recipe runs from m.provisioning. Installing VMs are read
// straight from m.vms rather than a tracked map, because the install starts
// itself the moment the VM boots; there is no "p" press to record one in
// m.provisioning the way a recipe run does. Each group is sorted by name so
// neither shuffles between frames.
func provLines(m model) []string { return provLinesExcept(m, "") }

// provLinesExcept is provLines with one VM dropped. viewDetail calls it with
// the selected VM's name: that VM already gets its own fuller panel from
// progressPanel, and repeating it here as a second, compact line would say
// the same thing twice on one screen.
func provLinesExcept(m model, except string) []string {
	inProvisioning := make(map[string]bool, len(m.provisioning))
	names := make([]string, 0, len(m.provisioning))
	for n := range m.provisioning {
		if n == except {
			continue
		}
		inProvisioning[n] = true
		names = append(names, n)
	}
	sort.Strings(names)

	var installing []core.VM
	for _, v := range m.vms {
		if v.Name != except && v.Mode == "disk" && !v.Installed && v.State == core.StateRunning && !inProvisioning[v.Name] {
			installing = append(installing, v)
		}
	}
	sort.Slice(installing, func(i, j int) bool { return installing[i].Name < installing[j].Name })

	if len(names) == 0 && len(installing) == 0 {
		return nil
	}

	now := time.Now()
	out := make([]string, 0, len(names)+len(installing))
	for _, v := range installing {
		out = append(out, installLine(m.spin, v, now))
	}
	for _, n := range names {
		out = append(out, provLine(m.spin, n, m.provisioning[n], now))
	}
	return out
}

// newSpinner is the one spinner in the program. MiniDot at its default rate
// reads as "working" without pulling the eye off the log line beside it.
func newSpinner() spinner.Model {
	s := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	s.Style = accentStyle
	return s
}

// vmByName finds a loaded VM by name. Provision runs are keyed by name, and
// a refresh replaces the row wholesale mid-run, so the log path must be
// resolved back through the current list.
//
// The key is the DIRECTORY. core.VM.Name reports the directory, and every
// caller here already keys its maps by it. A vm.toml whose name field has
// drifted from its directory does not split those maps in two.
func (m model) vmByName(name string) *core.VM {
	for i := range m.vms {
		if m.vms[i].Name == name {
			return &m.vms[i]
		}
	}
	return nil
}
