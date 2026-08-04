package tui

import (
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

// A cloud VM does most of its setup after the login prompt appears: the user
// is created, the password set and the packages installed by cloud-init,
// minutes into the boot. Until this was surfaced, "still installing",
// "finished" and "failed" all looked identical from stoat. A VM would reject
// a correct password for two minutes and then silently start accepting it,
// which is indistinguishable from broken. A real report.

// cloudInitMsg carries a polled status back to the model.
type cloudInitMsg struct {
	name   string
	status string // cloud-init's own word: running, done, error, disabled
}

// cloudInitPoll is how often an unfinished run is re-checked. Each poll is an
// ssh round trip, so this is deliberately slow: nobody is watching a package
// install second by second, and the terminal states stop the loop anyway.
const cloudInitPoll = 5 * time.Second

// cloudInitDone reports whether a status needs no further polling.
func cloudInitDone(status string) bool {
	switch status {
	case "done", "error", "disabled", "not-run":
		return true
	}
	return false
}

// checkCloudInit asks a running cloud VM how far cloud-init has got. It runs
// over the same ssh path everything else uses, so it inherits BatchMode and
// the short connect timeout: a guest that is not up yet fails fast and is
// reported as "waiting" rather than hanging the poll.
func checkCloudInit(v core.VM) tea.Cmd {
	name := v.Name
	return func() tea.Msg {
		out, err := exec.Command("ssh", sshx.Args(cfgVM(v), "cloud-init", "status")...).CombinedOutput()
		if err != nil {
			// Not reachable yet is the normal case for the first ~30 seconds
			// of a boot, so it is a state, not an error.
			return cloudInitMsg{name: name, status: "waiting"}
		}
		for _, l := range strings.Split(string(out), "\n") {
			if s, ok := strings.CutPrefix(strings.TrimSpace(l), "status:"); ok {
				return cloudInitMsg{name: name, status: strings.TrimSpace(s)}
			}
		}
		return cloudInitMsg{name: name, status: "unknown"}
	}
}

// pollCloudInit schedules the next check.
func pollCloudInit(v core.VM) tea.Cmd {
	return tea.Tick(cloudInitPoll, func(time.Time) tea.Msg { return cloudInitTickMsg{v.Name} })
}

type cloudInitTickMsg struct{ name string }

// cloudInitFraction maps cloud-init's state machine onto a bar. These are
// STAGES, not a measured percentage: cloud-init reports no progress number,
// and inventing one from elapsed time is the fake-% this codebase already
// refused for downloads. The word beside the bar always says which stage it
// is, so the bar adds shape rather than false precision.
func cloudInitFraction(status string) float64 {
	switch status {
	case "waiting":
		return 0.15
	case "running":
		return 0.6
	case "done", "error", "disabled", "not-run":
		return 1
	}
	return 0
}

// cloudInitBar renders the stage bar. Uses bubbles/progress rather than a
// hand-rolled track: it is driven with ViewAs from a value we compute, so
// none of the component's animation machinery runs, but the gradient and
// width handling come for free.
func cloudInitBar(p progress.Model, status string) string {
	if status == "" || cloudInitDone(status) && status != "running" {
		if status == "done" {
			return p.ViewAs(1)
		}
	}
	return p.ViewAs(cloudInitFraction(status))
}

// newCloudInitProgress is the bar used for cloud-init staging.
func newCloudInitProgress() progress.Model {
	p := fullBlockBar()
	p.SetWidth(accessValueWidth)
	return p
}

// cloudInitLabel renders a status for the access box, in the language of what
// it means for the user rather than cloud-init's own vocabulary.
func cloudInitLabel(status string) string {
	switch status {
	case "waiting":
		return dimStyle.Render("booting…")
	case "running":
		return warnStyle.Render("installing…")
	case "done":
		return upStyle.Render("ready")
	case "error":
		return errStyle.Render("failed: see cloud-init status --long in the vm")
	case "disabled", "not-run":
		return dimStyle.Render(status)
	case "":
		return ""
	default:
		return dimStyle.Render(status)
	}
}
