package core

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

func TestSnapshotsFrom(t *testing.T) {
	got := snapshotsFrom([]qemu.SnapshotEntry{
		{Name: "disk", DateSec: 1785844800},
		{Name: "old-ram", VMStateSize: 283 << 20, DateSec: 1785844800, DateNsec: 5},
	})
	if len(got) != 2 {
		t.Fatalf("got %d snapshots, want 2: %+v", len(got), got)
	}
	if got[0].VMState || got[0].SizeBytes != 0 {
		t.Errorf("disk-only snapshot reports memory: %+v", got[0])
	}
	if !got[1].VMState || got[1].SizeBytes != 283<<20 {
		t.Errorf("snapshot with a memory state reports none: %+v", got[1])
	}
	if got[0].Created.Location() != time.UTC || got[0].Created.Format(time.RFC3339) != "2026-08-04T12:00:00Z" {
		t.Errorf("Created = %v, want 2026-08-04T12:00:00Z in UTC", got[0].Created)
	}
}

// A stopped VM's snapshots go through the real qemu-img: the JSON field names
// are the contract snapshotsFrom depends on.
func TestSnapshotLifecycleOnStoppedDisk(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "d", Mode: "cloud", RAM: 512, CPUs: 1, SSHPort: 2200, Disk: "8G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), "64M").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}
	before := time.Now().Add(-time.Minute)
	if err := TakeSnapshot("d", "clean"); err != nil {
		t.Fatal(err)
	}
	snaps, err := Snapshots("d")
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].Tag != "clean" || snaps[0].VMState || snaps[0].SizeBytes != 0 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if snaps[0].Created.Before(before) || snaps[0].Created.After(time.Now().Add(time.Minute)) {
		t.Errorf("Created = %v, want about now", snaps[0].Created)
	}
	if err := Restore("d", "clean"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSnapshot("d", "clean"); err != nil {
		t.Fatal(err)
	}
	if snaps, _ = Snapshots("d"); len(snaps) != 0 {
		t.Errorf("snapshots after delete = %+v", snaps)
	}
}

// QEMU cannot revert an internal snapshot under a live disk. The refusal must
// name the fix and carry the already_running code, not surface QEMU's text.
func TestRestoreRefusesRunningVM(t *testing.T) {
	root(t)
	v := &config.VM{Name: "d", Mode: "cloud", RAM: 512, CPUs: 1, SSHPort: 2200, Disk: "8G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer realQemuProcess(t, v)()
	err := Restore("d", "clean")
	if !errors.Is(err, ErrAlreadyRunning) || !strings.Contains(err.Error(), "stoat down d") {
		t.Errorf("Restore = %v, want ErrAlreadyRunning naming stoat down d", err)
	}
}

// A live VM is diskless by design: an ISO booted into a tmpfs root. There is
// nowhere to put a snapshot and nothing that would survive one. Every entry
// point must refuse it, rather than fail later with a confusing qemu-img
// error about a missing file.
func TestSnapshotRefusesLiveVM(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "livevm", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := TakeSnapshot("livevm", "x"); !errors.Is(err, ErrNoDisk) {
		t.Errorf("TakeSnapshot = %v, want ErrNoDisk", err)
	}
	if err := Restore("livevm", "x"); !errors.Is(err, ErrNoDisk) {
		t.Errorf("Restore = %v, want ErrNoDisk", err)
	}
	if err := DeleteSnapshot("livevm", "x"); !errors.Is(err, ErrNoDisk) {
		t.Errorf("DeleteSnapshot = %v, want ErrNoDisk", err)
	}
	if _, err := Snapshots("livevm"); !errors.Is(err, ErrNoDisk) {
		t.Errorf("Snapshots = %v, want ErrNoDisk", err)
	}
}

// Whitespace in a tag is refused, not mangled.
func TestSnapshotRejectsBadTags(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "d", Mode: "disk", RAM: 512, CPUs: 1, SSHPort: 2200, Disk: "8G"}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"", "   ", "two words", "tab\there", "new\nline"} {
		if err := TakeSnapshot("d", tag); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("TakeSnapshot(tag=%q) = %v, want ErrInvalidSpec", tag, err)
		}
	}
}

// An unknown tag used to reach qemu, which answered in its own prose and
// mapped to the internal code. It is the caller's mistake, so it is not_found.
func TestRestoreAndDeleteRejectAnUnknownTag(t *testing.T) {
	// The stopped-VM path reads the tag list with qemu-img. Without it the
	// lookup fails before the tag is ever compared.
	haveQemuImg(t)
	root(t)
	// A cloud VM that never started has no qcow2, so Snapshots reports none
	// rather than failing. That is the empty case the message distinguishes.
	if err := (&config.VM{Name: "d", Mode: "cloud", RAM: 512, CPUs: 1, SSHPort: 2200, Disk: "8G"}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"Restore", func() error { return Restore("d", "nope") }},
		{"DeleteSnapshot", func() error { return DeleteSnapshot("d", "nope") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s = %v, want ErrNotFound", tc.name, err)
			}
			if !strings.Contains(err.Error(), "nope") {
				t.Fatalf("%s = %v, want the tag named", tc.name, err)
			}
		})
	}
}

func TestSnapshotUnknownVM(t *testing.T) {
	root(t)
	if err := TakeSnapshot("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := Snapshots("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
