package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// resetSeq is the SGR reset sequence lipgloss emits at the end of a styled
// substring. v1 wrote it as "\x1b[0m"; v2's ANSI writer omits the redundant
// "0" parameter and writes "\x1b[m" instead — both mean the same thing to a
// terminal, but a literal string match has to pick one.
const resetSeq = "\x1b[m"

// TestSelectedRowIsFullyHighlighted covers a highlight that was silently
// dead: the row was built starting with the styled status dot, and a styled
// substring ends in \x1b[0m, which resets the ENCLOSING style too. Wrapping
// that row in selStyle therefore left everything after the dot unstyled, so
// the selected row was marked by the ❯ alone. The row's own colours make it
// invisible in a plain string comparison, hence the escape-sequence check.
func TestSelectedRowIsFullyHighlighted(t *testing.T) {
	// v1 forced true-color output here because a Renderer could otherwise
	// downsample styles for a "dumb" terminal (relevant in CI, where stdout
	// isn't a tty). v2 removed the Renderer entirely — Style is a plain
	// value type that always renders full ANSI escapes, so there is nothing
	// left to force.
	vms := []*config.VM{
		{Name: "alpha", Mode: "live", RAM: 4096, CPUs: 4, SSHPort: 2200, Dir: t.TempDir()},
		{Name: "beta", Mode: "disk", RAM: 2048, CPUs: 2, SSHPort: 2201, Dir: t.TempDir()},
	}
	m := model{screen: screenList, width: 100, height: 30, list: newVMList(), vms: vms}
	m.list.SetItems(vmItems(vms, nil))
	m.syncListHeight()
	out := m.viewList()

	i := strings.Index(out, "alpha")
	if i < 0 {
		t.Fatal("selected vm name missing from the rendered list")
	}
	// Walk back to the escape sequence immediately preceding the name. It
	// must be a style being switched ON, not a reset.
	prefix := out[:i]
	esc := strings.LastIndex(prefix, "\x1b[")
	if esc < 0 {
		t.Fatal("no escape sequence before the selected row's name")
	}
	if strings.HasPrefix(prefix[esc:], resetSeq) {
		t.Error("selected row's name is preceded by a reset: the highlight dies at the status dot")
	}
	if !strings.Contains(prefix[esc:], "1;") {
		t.Errorf("selected row's name is not bold; preceding sequence was %q", prefix[esc:])
	}

	// The unselected row must NOT be bold, or the cursor tells you nothing.
	j := strings.Index(out, "beta")
	if j < 0 {
		t.Fatal("second vm missing from the rendered list")
	}
	seg := out[i:j]
	if strings.Count(seg, resetSeq) == 0 {
		t.Error("selected row's style is never closed before the next row")
	}
}

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
