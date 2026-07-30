package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestDeleteTargetsDirectoryNotName proves the fix for the "d" handler
// deleting the wrong VM: config.Load is DIRECTORY-keyed, but a vm.toml's
// "name" field can diverge from its directory (reachable through the "e"
// edit path). Two VM directories are built here, "work" and "work2", where
// work/vm.toml claims name="work2" — exactly the reproduction from the
// review. Deleting the *config.VM the cursor was actually on (work) must
// remove the work directory and leave work2 untouched, regardless of what
// either vm.toml's name field says.
func TestDeleteTargetsDirectoryNotName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	workDir := filepath.Join(root, "work")
	work2Dir := filepath.Join(root, "work2")

	work := &config.VM{Name: "work2", Mode: "live", Dir: workDir} // name diverges from dir
	if err := work.Save(); err != nil {
		t.Fatalf("saving work: %v", err)
	}
	work2 := &config.VM{Name: "work2", Mode: "live", Dir: work2Dir}
	if err := work2.Save(); err != nil {
		t.Fatalf("saving work2: %v", err)
	}

	// Simulate the cursor sitting on the "work" row and "d" then "y" being
	// pressed: pendingDelete holds the *config.VM object itself, not a name.
	m := model{pendingDelete: work}
	cmd := deleteVM(m.pendingDelete)
	cmd() // run the tea.Cmd synchronously; the return value only carries the status text

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("work directory should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(work2Dir); err != nil {
		t.Fatalf("work2 directory should still exist: %v", err)
	}
}
