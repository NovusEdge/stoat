package tui

import (
	"encoding/json"
	"os/exec"
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

// cloudInitJSON is the shape of `cloud-init status --format json` that this
// package reads. cloud-init emits more keys than this (datasource,
// boot_status_code, recoverable_errors, ...); the rest decode into nothing
// and are dropped, which is fine since only these three drive a state.
type cloudInitJSON struct {
	Status         string   `json:"status"`
	ExtendedStatus string   `json:"extended_status"`
	Errors         []string `json:"errors"`
}

// decodeCloudInitStatus turns the JSON `cloud-init status --format json`
// prints into this file's state vocabulary. It gates readiness on the
// errors list, never on the status or extended_status string: Alpine's
// cloud-init always reports extended_status "degraded" because
// keys_to_console looks for a helper binary Alpine's aport does not ship,
// which surfaces as a recoverable_errors warning with errors left empty.
// Reading extended_status directly would call that VM broken when it is
// fine. Undecodable input (an older cloud-init that rejects the --format
// flag, or anything else that is not this shape) maps to "unknown" rather
// than a guess.
func decodeCloudInitStatus(out []byte) string {
	var s cloudInitJSON
	if err := json.Unmarshal(out, &s); err != nil {
		return "unknown"
	}
	if len(s.Errors) > 0 {
		return "error"
	}
	switch s.Status {
	case "running":
		return "running"
	case "done":
		return "done"
	case "error":
		return "error"
	case "disabled":
		return "disabled"
	case "not run":
		return "not-run"
	}
	return "unknown"
}

// checkCloudInit asks a running cloud VM how far cloud-init has got. It runs
// over the same ssh path everything else uses, so it inherits BatchMode and
// the short connect timeout: a guest that is not up yet fails fast and is
// reported as "waiting" rather than hanging the poll.
//
// cloud-init status exits non-zero for its own error/degraded states even
// though it printed valid JSON, so a non-zero exit is not by itself
// "unreachable". ssh reserves exit code 255 for its own connection failures
// (see ssh(1)) and otherwise passes the remote command's exit code through,
// so that code is what separates "never got there" from "got there, ran,
// exited unhappy".
//
// Output(), not CombinedOutput(): a login banner or stray warning on stderr
// would land ahead of the JSON in a combined stream and break the decoder.
// Output() hands the decoder stdout alone and still returns an
// *exec.ExitError on a non-zero exit, so the 255 check below is unaffected.
func checkCloudInit(v core.VM) tea.Cmd {
	name := v.Name
	return func() tea.Msg {
		out, err := exec.Command("ssh", sshx.Args(cfgVM(v), "cloud-init", "status", "--format", "json")...).Output()
		if exitErr, ok := err.(*exec.ExitError); err != nil && (!ok || exitErr.ExitCode() == 255) {
			// Not reachable yet is the normal case for the first ~30 seconds
			// of a boot, so it is a state, not an error.
			return cloudInitMsg{name: name, status: "waiting"}
		}
		return cloudInitMsg{name: name, status: decodeCloudInitStatus(out)}
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
