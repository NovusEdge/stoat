package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestCmdlineMatchesDirBoundary guards against a name-collision false positive:
// "work" is a byte-prefix of "work2", so a bare substring check on the cmdline
// would report work2's live QEMU process as belonging to work.
func TestCmdlineMatchesDirBoundary(t *testing.T) {
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work")
	work2Dir := filepath.Join(tmp, "work2")

	// Synthesize a cmdline as QEMU would produce it for work2's process.
	cmdline := []byte(fmt.Sprintf("qemu-system-x86_64\x00-pidfile\x00%s/qemu.pid\x00", work2Dir))

	if cmdlineMatches(cmdline, workDir) {
		t.Errorf("cmdlineMatches(work2's cmdline, %q) = true, want false (name-collision false positive)", workDir)
	}
	if !cmdlineMatches(cmdline, work2Dir) {
		t.Errorf("cmdlineMatches(work2's cmdline, %q) = false, want true", work2Dir)
	}
}

// TestDiskWritten pins the two sizes that decide whether Start flips a disk VM
// to installed: an untouched qcow2 (~200 KB of metadata, which is what a VM
// whose setup-alpine never reached the disk step looks like) must not count.
func TestDiskWritten(t *testing.T) {
	v := &config.VM{Name: "work", Mode: "disk", Dir: t.TempDir()}

	if diskWritten(v) {
		t.Error("a VM with no disk image at all reports as written")
	}

	write := func(n int) {
		if err := os.WriteFile(v.DiskPath(), make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(196736) // a real, freshly created 8G qcow2
	if diskWritten(v) {
		t.Error("an empty qcow2 reports as written; the VM would skip its installer")
	}

	write(installedBytes + 1)
	if !diskWritten(v) {
		t.Error("an installed-size qcow2 does not report as written")
	}
}
