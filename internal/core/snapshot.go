package core

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// Snapshot is one saved state of a VM's disk. Stoat only takes disk-only
// snapshots.
type Snapshot struct {
	Tag string
	// VMState reports whether the snapshot also holds guest memory. Only a
	// snapshot taken by an earlier stoat version does; restoring one still
	// restores the disk only.
	VMState bool
	// SizeBytes is the size of the saved memory state. QEMU records no size
	// for the disk part of an internal snapshot, so it is 0 for a disk-only
	// snapshot.
	SizeBytes int64
	Created   time.Time
}

// ErrNoDisk is returned for a VM that has no qcow2 to put a snapshot in.
//
// A live-mode VM is diskless by design: it boots an ISO into a tmpfs root.
// There is nowhere to store a snapshot, and nothing in it would survive one.
// The text says "snapshots" rather than "to snapshot" because restore and
// list share this error, and "no disk to snapshot" read as the wrong
// operation when a restore hit it.
var ErrNoDisk = fmt.Errorf("no disk for snapshots")

// TakeSnapshot saves VM name's disk under tag.
//
// Go cannot have both a type and a function named Snapshot in one package,
// so the function is TakeSnapshot; the type above keeps the plain name.
//
// A stopped VM uses `qemu-img snapshot -c`. A running VM takes an internal
// snapshot over QMP, because qemu-img refuses an image QEMU has open. That
// snapshot is crash-consistent, as if the guest lost power: there is no guest
// agent to flush its filesystems first.
func TakeSnapshot(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return qemu.SnapshotSave(v, tag)
	}
	return qemuImgSnapshot(v, "-c", tag)
}

// Restore rolls VM name's disk back to the snapshot named tag; the next start
// boots from there.
//
// It refuses a running VM: QEMU has no command that reverts an internal
// snapshot under a live disk, and loadvm is blocked by the work share.
//
// Restore is destructive. It discards everything since the snapshot with no
// second copy. A caller that wraps this must warn before calling it.
func Restore(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return fmt.Errorf("%w: %s: stop the VM first (stoat down %s)", ErrAlreadyRunning, name, name)
	}
	if err := requireSnapshot(name, tag); err != nil {
		return err
	}
	return qemuImgSnapshot(v, "-a", tag)
}

// requireSnapshot rejects a tag the VM does not have, before qemu does. Both
// backends answer an unknown tag with their own prose, which reached a caller
// as an unmapped internal failure rather than the caller's own mistake. The
// known tags come back in the message because a wrong tag is usually a typo
// of a right one.
func requireSnapshot(name, tag string) error {
	snaps, err := Snapshots(name)
	if err != nil {
		return err
	}
	tags := make([]string, 0, len(snaps))
	for _, s := range snaps {
		if s.Tag == tag {
			return nil
		}
		tags = append(tags, s.Tag)
	}
	if len(tags) == 0 {
		return fmt.Errorf("%w: %s has no snapshot %q, and none at all", ErrNotFound, name, tag)
	}
	return fmt.Errorf("%w: %s has no snapshot %q; it has %s", ErrNotFound, name, tag, strings.Join(tags, ", "))
}

// DeleteSnapshot removes one snapshot and frees its space in the qcow2. It
// is not part of Destroy's naming family: deleting a VM and deleting a
// snapshot are very different sizes of mistake.
func DeleteSnapshot(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if err := requireSnapshot(name, tag); err != nil {
		return err
	}
	if qemu.Running(v) {
		return qemu.SnapshotDelete(v, tag)
	}
	return qemuImgSnapshot(v, "-d", tag)
}

// Snapshots lists what VM name has saved.
//
// A running VM is queried over QMP, not by reading its qcow2 directly. QEMU
// holds the image open and writes to it, so qemu-img can report stale state.
func Snapshots(name string) ([]Snapshot, error) {
	v, err := load(name)
	if err != nil {
		return nil, err
	}
	if err := RequireCapability(v, capabilities.OpSnapshot); err != nil {
		return nil, err
	}
	if v.Mode == "live" {
		return nil, fmt.Errorf("%w: %s is a live VM", ErrNoDisk, name)
	}

	var entries []qemu.SnapshotEntry
	if qemu.Running(v) {
		entries, err = qemu.SnapshotList(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	} else {
		b, err := exec.Command("qemu-img", "info", "--output=json", v.DiskPath()).CombinedOutput()
		if err != nil {
			// A cloud VM that never started has no qcow2 yet; the overlay is
			// created on first start (see core.Create). That is "no
			// snapshots", not a failure.
			if strings.Contains(string(b), "No such file") {
				return nil, nil
			}
			return nil, fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(string(b)))
		}
		var info struct {
			Snapshots []qemu.SnapshotEntry `json:"snapshots"`
		}
		if err := json.Unmarshal(b, &info); err != nil {
			return nil, fmt.Errorf("%s: qemu-img info: %w", name, err)
		}
		entries = info.Snapshots
	}
	return snapshotsFrom(entries), nil
}

func snapshotsFrom(entries []qemu.SnapshotEntry) []Snapshot {
	snaps := make([]Snapshot, len(entries))
	for i, e := range entries {
		snaps[i] = Snapshot{
			Tag:       e.Name,
			VMState:   e.VMStateSize > 0,
			SizeBytes: e.VMStateSize,
			Created:   time.Unix(e.DateSec, e.DateNsec).UTC(),
		}
	}
	return snaps
}

// snapshotTarget resolves name and rejects the cases no snapshot can work on,
// so each operation above does not repeat the checks.
func snapshotTarget(name, tag string) (*config.VM, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, fmt.Errorf("%w: a snapshot needs a tag", ErrInvalidSpec)
	}
	// A tag is a bare word on every surface (CLI argument, MCP field, TUI
	// list); whitespace in one could not be typed back to restore it.
	if strings.ContainsAny(tag, " \t\n") {
		return nil, fmt.Errorf("%w: snapshot tag %q cannot contain whitespace", ErrInvalidSpec, tag)
	}
	v, err := load(name)
	if err != nil {
		return nil, err
	}
	if err := RequireCapability(v, capabilities.OpSnapshot); err != nil {
		return nil, err
	}
	if v.Mode == "live" {
		return nil, fmt.Errorf("%w: %s is a live VM (diskless by design)", ErrNoDisk, name)
	}
	return v, nil
}

func qemuImgSnapshot(v *config.VM, flag, tag string) error {
	out, err := exec.Command("qemu-img", "snapshot", flag, tag, v.DiskPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
