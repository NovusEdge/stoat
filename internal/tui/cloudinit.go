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

// A cloud VM finishes its setup after the login prompt appears. cloud-init
// creates the user, sets the password and installs packages, minutes into
// the boot. Without a status check, "still installing", "finished" and
// "failed" all look the same from stoat: a correct password is rejected for
// up to two minutes, then silently accepted.

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
// package reads. cloud-init emits more keys (datasource, boot_status_code,
// recoverable_errors, ...); the rest decode into nothing and are dropped.
// Only these three fields drive a state.
type cloudInitJSON struct {
	Status         string   `json:"status"`
	ExtendedStatus string   `json:"extended_status"`
	Errors         []string `json:"errors"`
}

// decodeCloudInitStatus turns the JSON `cloud-init status --format json`
// prints into this file's state vocabulary. It gates readiness on the
// errors list, never on the status or extended_status string.
//
// Alpine's cloud-init always reports extended_status "degraded". Its
// keys_to_console module looks for a helper binary Alpine's aport does not
// ship, which surfaces as a recoverable_errors warning with errors left
// empty. Reading extended_status directly would call that VM broken when it
// is fine.
//
// Undecodable input maps to "unknown", not a guess. This covers an older
// cloud-init that rejects the --format flag, and anything else that is not
// this shape.
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
// the short connect timeout. A guest that is not up yet fails fast and is
// reported as "waiting", not left to hang the poll.
//
// cloud-init status exits non-zero for its own error and degraded states
// even after printing valid JSON, so a non-zero exit alone does not mean
// "unreachable". ssh reserves exit code 255 for its own connection failures
// (see ssh(1)) and otherwise passes the remote command's exit code through.
// That code separates "never got there" from "got there, ran, exited
// unhappy".
//
// checkCloudInit calls Output(), not CombinedOutput(). A login banner or
// stray stderr warning would land ahead of the JSON in a combined stream and
// break the decoder. Output() hands the decoder stdout alone, and still
// returns an *exec.ExitError on a non-zero exit, so the 255 check above
// still works.
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

// cloudInitFraction maps cloud-init's state machine onto a bar. These
// numbers are stages, not a measured percentage: cloud-init reports no
// progress number. Inventing one from elapsed time is the fake percentage
// this codebase already refused for downloads. The word beside the bar
// always names the stage, so the bar adds shape, not false precision.
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

// cloudInitBar renders the stage bar with bubbles/progress instead of a
// hand-rolled track. ViewAs draws a fixed computed value, so none of the
// component's animation machinery runs. The gradient and width handling
// still come free.
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
