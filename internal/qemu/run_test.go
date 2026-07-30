package qemu

import (
	"fmt"
	"path/filepath"
	"testing"
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
