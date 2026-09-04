package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// qmpTimeout bounds a whole QMP exchange. Snapshotting a running VM writes the
// guest's entire RAM into the qcow2, so this is generous by design: a 4GB VM
// on a slow disk legitimately takes tens of seconds. It exists to stop a
// wedged QEMU hanging the caller forever, not to police normal work.
const qmpTimeout = 5 * time.Minute

// qmp is a connected QMP session. QEMU serves it on a second socket alongside
// the human monitor (see args.go): the human monitor is a REPL whose replies
// are prose interleaved with prompts, which is fine for fire-and-forget
// commands like system_powerdown but not for an operation that can destroy
// guest state and whose failure must be told apart from its output. QMP frames
// every reply as JSON with an explicit error object.
type qmp struct {
	c   net.Conn
	dec *json.Decoder
}

// dialQMP connects and completes the capabilities handshake QMP requires
// before it will accept any command: the server sends a greeting, the client
// must answer with qmp_capabilities, and only then does the session leave
// negotiation mode. Skipping it makes every subsequent command fail with
// "Expecting capabilities negotiation", which is a confusing way to learn this.
func dialQMP(v *config.VM) (*qmp, error) {
	c, err := net.Dial("unix", v.QMPPath())
	if err != nil {
		return nil, fmt.Errorf("%w: qmp: %w", ErrMonitorUnreachable, err)
	}
	if err := c.SetDeadline(time.Now().Add(qmpTimeout)); err != nil {
		_ = c.Close()
		return nil, err
	}
	q := &qmp{c: c, dec: json.NewDecoder(c)}

	// The greeting arrives unsolicited; it must be consumed before the
	// handshake reply, or the decoder reads it as the answer to it.
	var greeting struct {
		QMP json.RawMessage `json:"QMP"`
	}
	if err := q.dec.Decode(&greeting); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w: qmp greeting: %w", ErrMonitorUnreachable, err)
	}
	if greeting.QMP == nil {
		_ = c.Close()
		return nil, fmt.Errorf("%w: qmp: not a QMP socket (no greeting)", ErrMonitorUnreachable)
	}
	if _, err := q.command("qmp_capabilities", nil); err != nil {
		_ = c.Close()
		return nil, err
	}
	return q, nil
}

func (q *qmp) Close() error { return q.c.Close() }

// command sends one QMP command and returns its "return" value.
//
// QEMU may emit asynchronous EVENTS (a guest powering down, a job completing)
// at any moment, including between a command and its reply. They are skipped
// here rather than surfaced: nothing in stoat subscribes to events, and a
// caller that treated the first message after a command as its reply would
// intermittently read an event instead, a race that only shows up under
// exactly the conditions snapshots create.
func (q *qmp) command(name string, args map[string]any) (json.RawMessage, error) {
	req := map[string]any{"execute": name}
	if args != nil {
		req["arguments"] = args
	}
	if err := json.NewEncoder(q.c).Encode(req); err != nil {
		return nil, fmt.Errorf("qmp %s: %w", name, err)
	}
	for {
		var resp struct {
			Return json.RawMessage `json:"return"`
			Error  *struct {
				Class string `json:"class"`
				Desc  string `json:"desc"`
			} `json:"error"`
			Event string `json:"event"`
		}
		if err := q.dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("qmp %s: %w", name, err)
		}
		if resp.Event != "" {
			continue // asynchronous event, not our reply
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%w: qmp %s: %s", ErrMonitorRejected, name, resp.Error.Desc)
		}
		return resp.Return, nil
	}
}

// hmp runs a human-monitor command through QMP and returns its text output.
//
// Snapshots are the reason this exists. QMP's own snapshot API
// (snapshot-save/snapshot-load, QEMU 6.0+) is an ASYNCHRONOUS JOB interface:
// the command returns immediately and the caller must poll query-jobs for
// completion, then interpret job states. human-monitor-command runs the
// long-standing savevm/loadvm/delvm synchronously and hands back whatever the
// monitor printed, far less machinery for an operation stoat performs one at
// a time and waits on anyway.
//
// The tradeoff, stated plainly: HMP reports failure as TEXT, so the caller has
// to inspect the string. What QMP still buys over talking to the human monitor
// socket directly is framing: the reply is exactly this command's output,
// delimited, with transport errors distinguishable from command output. That
// is the part the human monitor's prompt-interleaved stream cannot give.
func (q *qmp) hmp(cmd string) (string, error) {
	raw, err := q.command("human-monitor-command", map[string]any{"command-line": cmd})
	if err != nil {
		return "", err
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("qmp %q: %w", cmd, err)
	}
	return out, nil
}

// hmpChecked runs an HMP command that is expected to print NOTHING on success.
// savevm, loadvm and delvm all follow that convention, so any output at all is
// the error message, which is the only way to detect failure over HMP, since
// the QMP layer itself succeeded in running the command.
func (q *qmp) hmpChecked(cmd string) error {
	out, err := q.hmp(cmd)
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(out); s != "" {
		return fmt.Errorf("%s", s)
	}
	return nil
}

// snapshotCmd dials QMP, runs an HMP command that prints nothing on success,
// and closes the session. savevm, loadvm and delvm all share this shape.
func snapshotCmd(v *config.VM, hmp string) error {
	q, err := dialQMP(v)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	return q.hmpChecked(hmp)
}

// SnapshotSave takes a live snapshot of a RUNNING VM, capturing RAM as well as
// disk, so restoring resumes execution rather than rebooting.
func SnapshotSave(v *config.VM, tag string) error {
	return snapshotCmd(v, "savevm "+tag)
}

// SnapshotLoad restores a RUNNING VM to a snapshot.
func SnapshotLoad(v *config.VM, tag string) error {
	return snapshotCmd(v, "loadvm "+tag)
}

// SnapshotDelete removes a snapshot from a RUNNING VM's disk.
func SnapshotDelete(v *config.VM, tag string) error {
	return snapshotCmd(v, "delvm "+tag)
}

// SnapshotInfo returns the raw "info snapshots" text from a RUNNING VM.
//
// It is not read from the qcow2 with qemu-img while the VM is up: QEMU holds
// the image open and writes to it, and qemu-img explicitly warns that
// inspecting an image in use can report inconsistent state. Asking the process
// that owns the file is the answer that is actually true.
func SnapshotInfo(v *config.VM) (string, error) {
	q, err := dialQMP(v)
	if err != nil {
		return "", err
	}
	defer func() { _ = q.Close() }()
	return q.hmp("info snapshots")
}
