package core

import (
	"errors"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestParseSnapshotsRealOutput parses QEMU's actual table output, not an
// invented sample. The columns are padded to fit their content, so a fixed
// byte-offset parse breaks the first time a tag is longer than the header.
func TestParseSnapshotsRealOutput(t *testing.T) {
	// CAPTURED VERBATIM from `qemu-img snapshot -l` on a real qcow2
	// (qemu-img 11.0.2, 2026-08-04), not written from memory.
	stopped := `Snapshot list:
ID      TAG               VM_SIZE                DATE        VM_CLOCK     ICOUNT
1       clean                 0 B 2026-08-04 00:21:33  0000:00:00.000          0
2       before-upgrade        0 B 2026-08-04 00:21:33  0000:00:00.000          0
`
	got := parseSnapshots(stopped)
	if len(got) != 2 {
		t.Fatalf("parsed %d snapshots, want 2: %+v", len(got), got)
	}
	if got[0].Tag != "clean" || got[1].Tag != "before-upgrade" {
		t.Errorf("tags = %q, %q", got[0].Tag, got[1].Tag)
	}
	for _, s := range got {
		if s.VMState {
			t.Errorf("%q reports VMState on a 0 B snapshot; a stopped snapshot captures no RAM", s.Tag)
		}
	}

	// CAPTURED VERBATIM over QMP from a running Alpine VM (2026-08-04),
	// CRLF endings included. The ID and ICOUNT columns are "--", not
	// numeric: a running VM's snapshots have no numeric ID. A parser that
	// requires one silently drops every snapshot from a running VM.
	running := "List of snapshots present on all disks:\r\n" +
		"ID      TAG               VM_SIZE                DATE        VM_CLOCK     ICOUNT\r\n" +
		"--      live              283 MiB 2026-08-04 00:24:24  0000:00:40.074         --\r\n"
	got = parseSnapshots(running)
	if len(got) != 1 {
		t.Fatalf("parsed %d snapshots, want 1: %+v", len(got), got)
	}
	if !got[0].VMState {
		t.Error("a 203 MiB snapshot must report VMState: restoring it resumes execution rather than rebooting")
	}
	if got[0].Size != "283 MiB" {
		t.Errorf("Size = %q, want %q", got[0].Size, "283 MiB")
	}
	if got[0].Created != "2026-08-04 00:24:24" {
		t.Errorf("Created = %q", got[0].Created)
	}
}

// A long tag shifts every column after it. This is the case that breaks any
// fixed-width parse, and long tags are exactly what people write.
func TestParseSnapshotsLongTagShiftsColumns(t *testing.T) {
	// The column layout is real qemu-img output; the long tag genuinely does
	// push every following column right, which is why parsing is by fields.
	out := `ID      TAG               VM_SIZE                DATE        VM_CLOCK     ICOUNT
2       a-really-long-snapshot-tag-name      1.5 GiB 2026-08-04 00:21:33  0000:00:00.000          0
`
	got := parseSnapshots(out)
	if len(got) != 1 {
		t.Fatalf("parsed %d, want 1: %+v", len(got), got)
	}
	if got[0].Tag != "a-really-long-snapshot-tag-name" {
		t.Errorf("Tag = %q", got[0].Tag)
	}
	if got[0].Size != "1.5 GiB" || !got[0].VMState {
		t.Errorf("Size = %q, VMState = %v", got[0].Size, got[0].VMState)
	}
}

// Headers, preambles and blank lines must not become snapshots. An empty list
// is a normal state, not an error.
func TestParseSnapshotsIgnoresNonRows(t *testing.T) {
	for _, out := range []string{
		"",
		"Snapshot list:\n",
		"List of snapshots present on all disks:\nID        TAG                     VM SIZE\n",
	} {
		if got := parseSnapshots(out); len(got) != 0 {
			t.Errorf("parseSnapshots(%q) = %+v, want none", out, got)
		}
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

// A tag reaches the monitor as a bare word in "savevm <tag>". Whitespace in
// it would be read as extra arguments. Refused rather than mangled, the
// same rule passwordKeys applies to sendkey.
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

func TestSnapshotUnknownVM(t *testing.T) {
	root(t)
	if err := TakeSnapshot("nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := Snapshots("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
