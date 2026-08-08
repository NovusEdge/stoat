package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// provState is what the UI knows about one in-flight provision run. There is
// no percentage: the run installs an unknown number of packages over ssh, so
// any number shown would be invented. The UI shows which recipe is running,
// its last printed line, and how long the run has taken.
type provState struct {
	start time.Time
	step  string // recipe name, or the phase before one starts
	last  string // the log's most recent output line
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
// Returns the step (which recipe, or the phase before one starts) and the most
// recent line of real output.
func readProvStep(v core.VM) (step, last string) {
	b := tailBytes(v.Paths.ApplyLog, provTailBytes)
	if len(b) == 0 {
		return "starting", ""
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
	return step, last
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

// provLine renders one in-flight run: spinner, VM, current step, last output,
// elapsed. Everything in it is derived from real state: there is no progress
// bar here on purpose, because nothing knows how many steps remain.
func provLine(spin spinner.Model, name string, st provState, now time.Time) string {
	out := spin.View() + " " + accentStyle.Render(name) + dimStyle.Render(" · "+st.step)
	if st.last != "" {
		l := st.last
		if len(l) > provMaxLast {
			l = l[:provMaxLast-1] + "…"
		}
		out += dimStyle.Render(" · " + l)
	}
	return out + dimStyle.Render(" · "+provElapsed(now.Sub(st.start)))
}

// provLines renders every in-flight run, newest state first read fresh from
// the logs. Sorted by name so the order doesn't shuffle between frames.
func provLines(m model) []string {
	if len(m.provisioning) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.provisioning))
	for n := range m.provisioning {
		names = append(names, n)
	}
	sort.Strings(names)

	now := time.Now()
	out := make([]string, 0, len(names))
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
