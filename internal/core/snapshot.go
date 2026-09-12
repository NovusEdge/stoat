package core

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// Snapshot is one saved state of a VM's disk, and possibly its RAM.
type Snapshot struct {
	Tag string
	// VMState reports whether the snapshot captured the guest's memory. A
	// snapshot taken while the VM ran resumes execution where it left off.
	// One taken while stopped restores the disk only; the guest boots
	// fresh. The two are not interchangeable.
	VMState bool
	Size    string // as qemu reports it; free-form, for display only
	Created string
}

// ErrNoDisk is returned for a VM that has no qcow2 to put a snapshot in.
//
// A live-mode VM is diskless by design: it boots an ISO into a tmpfs root.
// There is nowhere to store a snapshot, and nothing in it would survive one.
// The text says "snapshots" rather than "to snapshot" because restore and
// list share this error, and "no disk to snapshot" read as the wrong
// operation when a restore hit it.
var ErrNoDisk = fmt.Errorf("no disk for snapshots")

// TakeSnapshot saves VM name's current state under tag.
//
// Go cannot have both a type and a function named Snapshot in one package,
// so the function is TakeSnapshot; the type above keeps the plain name.
//
// The mechanism depends on VM state. A stopped VM uses `qemu-img snapshot
// -c`: no process holds the image open. A running VM uses QMP savevm, which
// captures RAM as well as disk; qemu-img refuses to write an image QEMU has
// open, and forcing it would snapshot a torn filesystem.
//
// The two produce different results. See Snapshot.VMState.
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

// Restore resets VM name to the snapshot named tag.
//
// On a running VM this resumes execution inside the snapshot; the guest
// does not reboot. On a stopped VM it rolls the disk back, and the next
// start boots from there.
//
// Restore is destructive. It discards everything since the snapshot with no
// second copy. A caller that wraps this must warn before calling it.
func Restore(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if err := requireSnapshot(name, tag); err != nil {
		return err
	}
	if qemu.Running(v) {
		return qemu.SnapshotLoad(v, tag)
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
// holds the image open and writes to it, so qemu-img can report stale
// state. Both paths return QEMU's same "info snapshots" table, parsed by
// parseSnapshots.
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

	var out string
	if qemu.Running(v) {
		out, err = qemu.SnapshotInfo(v)
	} else {
		var b []byte
		b, err = exec.Command("qemu-img", "snapshot", "-l", v.DiskPath()).CombinedOutput()
		out = string(b)
	}
	if err != nil {
		// A cloud VM that never started has no qcow2 yet; the overlay is
		// created on first start (see core.Create). That is "no
		// snapshots", not a failure.
		if strings.Contains(out, "No such file") || strings.Contains(err.Error(), "no such file") {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(out))
	}
	return parseSnapshots(out), nil
}

// snapshotTarget resolves name and rejects the cases no snapshot can work on,
// so each operation above does not repeat the checks.
func snapshotTarget(name, tag string) (*config.VM, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, fmt.Errorf("%w: a snapshot needs a tag", ErrInvalidSpec)
	}
	// The tag reaches the monitor as a bare word in "savevm <tag>".
	// Whitespace in it would be read as extra arguments. Refused rather
	// than mangled.
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

// parseSnapshots reads QEMU's "info snapshots" / "qemu-img snapshot -l"
// table. Both commands print the same fixed-column format:
//
//	ID        TAG               VM_SIZE      DATE         VM_CLOCK  ICOUNT
//	1         clean              203 MiB  2026-08-04 12:00:00  00:00:12.345
//
// Parsing splits on fields, not byte offsets. The columns are padded to
// their content, so a long tag or large VM_SIZE shifts every column after
// it.
//
// A row is identified by its DATE column, not its ID. `qemu-img snapshot
// -l` numbers rows 1, 2, 3. The running VM's `info snapshots` prints "--"
// for both ID and ICOUNT. An earlier version required a numeric ID and
// silently dropped every snapshot belonging to a running VM.
//
// A VM_SIZE of "0 B" marks a disk-only snapshot: one taken while the VM was
// stopped captured no RAM. This is the fact Snapshot.VMState carries.
func parseSnapshots(out string) []Snapshot {
	var snaps []Snapshot
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// ID, TAG, VM_SIZE(value+unit), DATE, TIME at minimum.
		if len(f) < 6 {
			continue
		}
		// The DATE column is the discriminator. Headers ("VM_CLOCK" sits
		// there), preambles, and blank lines all fail it. Both producers
		// print the same YYYY-MM-DD there regardless of what they put in ID.
		if !isDate(f[4]) {
			continue
		}
		size := f[2] + " " + f[3]
		snaps = append(snaps, Snapshot{
			Tag:     f[1],
			VMState: !strings.HasPrefix(size, "0 "),
			Size:    size,
			Created: f[4] + " " + f[5],
		})
	}
	return snaps
}

// isDate reports whether s has the YYYY-MM-DD shape both `qemu-img
// snapshot -l` and `info snapshots` print in their DATE column.
//
// This is a shape check, not a time.Parse. It only decides "is this a data
// row". A row whose date QEMU formats slightly differently still gets
// listed, rather than silently dropped.
func isDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
