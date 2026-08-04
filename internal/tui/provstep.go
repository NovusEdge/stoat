package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"

	"github.com/novusedge/stoat/internal/config"
)

// provState is what the UI knows about one in-flight provision run. There is
// deliberately no percentage: the run is a shell script over ssh installing an
// unknown number of packages, so any number would be invented. What can be
// shown honestly is which recipe is running, what it last printed, and how
// long it has been going.
type provState struct {
	start time.Time
	step  string // recipe name, or the phase before one starts
	last  string // the log's most recent output line
}

// provTailBytes is how much of the end of last-provision.log gets read to work
// out the current step. A provision run that installs a desktop writes
// hundreds of KB; re-reading all of it several times a second to find the last
// line would be silly, and the tail is all that "current" means.
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

// recipeMarker is what sshx.Provision writes before each recipe. Parsing the
// log the run already produces beats plumbing progress through a channel: the
// provisioning goroutine stays a plain function, and the CLI's `stoat
// provision` gets the same information for free by streaming that same file.
const recipeMarker = "=== recipe "

// readProvStep derives the current step from the tail of a VM's provision log.
// Returns the step (which recipe, or the phase before one starts) and the most
// recent line of real output.
func readProvStep(v *config.VM) (step, last string) {
	b := tailBytes(v.ProvisionLogPath(), provTailBytes)
	if len(b) == 0 {
		return "starting", ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")

	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		if step == "" && strings.HasPrefix(l, recipeMarker) {
			step = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(l, recipeMarker), "==="))
		}
		if last == "" && !strings.HasPrefix(l, recipeMarker) {
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

// vmByName finds a loaded VM by name. Provision runs are keyed by name (the
// VM pointer can be replaced by a refresh mid-run), so the log path has to be
// resolved back through the current list.
func (m model) vmByName(name string) *config.VM {
	for _, v := range m.vms {
		if v.Name == name {
			return v
		}
	}
	return nil
}
