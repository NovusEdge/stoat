package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// qmpTimeout bounds a whole QMP exchange. It exists to stop a wedged QEMU
// hanging the caller forever, not to police normal work.
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

// SnapshotEntry is one internal snapshot as QEMU's ImageInfo reports it, both
// over QMP and from `qemu-img info --output=json`.
type SnapshotEntry struct {
	Name        string `json:"name"`
	VMStateSize int64  `json:"vm-state-size"`
	DateSec     int64  `json:"date-sec"`
	DateNsec    int64  `json:"date-nsec"`
}

// diskDevice finds the block device backed by the VM's qcow2. Its `device`
// name ("virtio0" for an unnamed -drive) is what the snapshot commands take,
// and args.go gives the drive no id to look it up by.
func (q *qmp) diskDevice(v *config.VM) (device string, snaps []SnapshotEntry, err error) {
	raw, err := q.command("query-block", nil)
	if err != nil {
		return "", nil, err
	}
	var blocks []struct {
		Device   string `json:"device"`
		Inserted *struct {
			File  string `json:"file"`
			Image struct {
				Snapshots []SnapshotEntry `json:"snapshots"`
			} `json:"image"`
		} `json:"inserted"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, fmt.Errorf("qmp query-block: %w", err)
	}
	disk := filepath.Clean(v.DiskPath())
	for _, b := range blocks {
		if b.Inserted != nil && filepath.Clean(b.Inserted.File) == disk {
			return b.Device, b.Inserted.Image.Snapshots, nil
		}
	}
	return "", nil, fmt.Errorf("%w: qmp: no block device backs %s", ErrMonitorRejected, disk)
}

// SnapshotSave takes a disk-only internal snapshot of a RUNNING VM's qcow2.
//
// savevm and snapshot-save cannot be used: QEMU refuses migration while the
// always-on `work` virtfs export is attached (args.go), and both go through
// the migration code. blockdev-snapshot-internal-sync touches only the image,
// so the guest keeps running and the snapshot is crash-consistent: the guest
// agent is not present to freeze filesystems first.
func SnapshotSave(v *config.VM, tag string) error {
	q, err := dialQMP(v)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	dev, _, err := q.diskDevice(v)
	if err != nil {
		return err
	}
	_, err = q.command("blockdev-snapshot-internal-sync", map[string]any{"device": dev, "name": tag})
	return err
}

// SnapshotDelete removes a snapshot from a RUNNING VM's qcow2.
func SnapshotDelete(v *config.VM, tag string) error {
	q, err := dialQMP(v)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	dev, _, err := q.diskDevice(v)
	if err != nil {
		return err
	}
	_, err = q.command("blockdev-snapshot-delete-internal-sync", map[string]any{"device": dev, "name": tag})
	return err
}

// SnapshotList returns the snapshots of a RUNNING VM's qcow2. It asks the
// process that owns the file: qemu-img on an image in use can report
// inconsistent state.
func SnapshotList(v *config.VM) ([]SnapshotEntry, error) {
	q, err := dialQMP(v)
	if err != nil {
		return nil, err
	}
	defer func() { _ = q.Close() }()
	_, snaps, err := q.diskDevice(v)
	return snaps, err
}
