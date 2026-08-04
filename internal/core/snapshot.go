package core

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// Snapshot is one saved state of a VM's disk, and possibly its RAM.
type Snapshot struct {
	Tag string
	// VMState reports whether the guest's MEMORY was captured too. A snapshot
	// taken while the VM ran resumes execution where it left off; one taken
	// while it was stopped restores the disk only and the guest boots. Both
	// are useful and they are not interchangeable, so a caller has to be able
	// to tell which it is holding.
	VMState bool
	Size    string // as qemu reports it; free-form, for display only
	Created string
}

// ErrNoDisk is returned for a VM that has no qcow2 to put a snapshot in.
//
// A live-mode VM is diskless by design: it boots an ISO into a tmpfs root, so
// there is nowhere for a snapshot to live and nothing that would survive one.
// This is a property of the mode, not a missing feature.
var ErrNoDisk = fmt.Errorf("no disk to snapshot")

// TakeSnapshot saves VM name's current state under tag.
//
// Named TakeSnapshot, not Snapshot as the design doc spells it: Snapshot is
// the RESULT type above, and Go will not let a package hold both. The type
// keeps the plain name because callers pass it around; the verb takes the
// longer one.
//
// Two mechanisms, chosen by state, exactly as the design settled
// (docs/design/core-api.md §8, decision 2):
//
//   - STOPPED: `qemu-img snapshot -c`. No running process is needed and
//     nothing has the image open, so this is the simple and safe path.
//   - RUNNING: QMP savevm, which captures RAM as well as disk. Doing it with
//     qemu-img instead would mean writing to an image QEMU has open and is
//     actively writing; qemu-img warns about exactly that, and the result
//     would be a snapshot of a torn filesystem.
//
// The two produce different things (see Snapshot.VMState) and that difference
// is reported rather than smoothed over.
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
// On a RUNNING VM this resumes execution inside the snapshot: the guest does
// not reboot, and anything it was doing since is gone. On a stopped VM it
// rolls the disk back and the next start boots from there.
//
// This is the operation the design doc calls the core testing loop: set it up,
// snapshot, break it, restore. It is also unambiguously destructive: whatever
// happened since the snapshot is discarded with no second copy, so a caller
// that wraps it should say so before calling.
func Restore(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return qemu.SnapshotLoad(v, tag)
	}
	return qemuImgSnapshot(v, "-a", tag)
}

// DeleteSnapshot removes one snapshot, freeing the space it holds in the
// qcow2. Named DeleteSnapshot rather than joining Destroy's family because
// deleting a VM and deleting one of its snapshots are very different sizes of
// mistake.
func DeleteSnapshot(name, tag string) error {
	v, err := snapshotTarget(name, tag)
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return qemu.SnapshotDelete(v, tag)
	}
	return qemuImgSnapshot(v, "-d", tag)
}

// Snapshots lists what VM name has saved.
//
// A running VM is asked over QMP rather than having its qcow2 read behind its
// back: QEMU holds the image open and writes to it, so qemu-img can report
// state that is already stale. Both paths come back through parseSnapshots,
// because both print QEMU's same "info snapshots" table.
func Snapshots(name string) ([]Snapshot, error) {
	v, err := load(name)
	if err != nil {
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
		// A VM that has never been started has no qcow2 at all in cloud mode
		// (the overlay is created on first start; see core.Create), which is
		// "no snapshots", not a failure.
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
	// The tag reaches the monitor as a bare word in "savevm <tag>", so a tag
	// with whitespace in it would be read as extra arguments and mean
	// something other than what was asked. Refused rather than mangled.
	if strings.ContainsAny(tag, " \t\n") {
		return nil, fmt.Errorf("%w: snapshot tag %q cannot contain whitespace", ErrInvalidSpec, tag)
	}
	v, err := load(name)
	if err != nil {
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

// parseSnapshots reads QEMU's "info snapshots" / "qemu-img snapshot -l" table.
//
// Both print the same fixed-column format:
//
//	ID        TAG               VM_SIZE      DATE         VM_CLOCK  ICOUNT
//	1         clean              203 MiB  2026-08-04 12:00:00  00:00:12.345
//
// Parsed by FIELDS rather than by byte offsets: the columns are padded to fit
// their content, so a long tag or a large VM_SIZE shifts everything after it
// and any fixed-width assumption breaks on exactly the snapshots people make.
//
// A row is identified by its DATE column, not by its ID. The ID differs
// between the two producers and that difference is invisible until you look at
// real output: `qemu-img snapshot -l` numbers them 1, 2, 3, while the running
// VM's `info snapshots` prints "--" for both ID and ICOUNT. An earlier version
// of this required a numeric ID and therefore silently dropped EVERY snapshot
// belonging to a running VM: `stoat snapshot <vm>` reported "no snapshots"
// immediately after successfully taking one. Caught only by running it against
// a real VM; the unit test could not, because its "running" sample was written
// from memory with a numeric ID that QEMU never prints.
//
// A VM_SIZE of "0 B" is what marks a disk-only snapshot, one taken while the
// VM was stopped captured no RAM. That is the distinction Snapshot.VMState
// carries, and it is derived here rather than guessed from context.
func parseSnapshots(out string) []Snapshot {
	var snaps []Snapshot
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// ID, TAG, VM_SIZE(value+unit), DATE, TIME at minimum.
		if len(f) < 6 {
			continue
		}
		// The DATE column is the discriminator: headers ("VM_CLOCK" sits
		// there), preambles and blank lines all fail it, and both producers
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

// isDate reports whether s is a YYYY-MM-DD date, the shape both `qemu-img
// snapshot -l` and the monitor's `info snapshots` print in their DATE column.
// Deliberately a shape check rather than a time.Parse: this decides "is this a
// data row", and a row whose date QEMU formats slightly differently should
// still be listed rather than silently vanishing.
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
