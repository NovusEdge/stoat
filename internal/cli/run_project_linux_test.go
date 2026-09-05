//go:build linux

package cli

import (
	"bytes"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// rm at project scope asks once per VM. Answering n for one VM leaves it and
// stops the run, the same as any other failure in the fan-out.
//
// This needs a real pty, not a strings.Reader: confirm refuses any stdin
// that terminal() does not report as a TTY, and openTestPTY (defined in
// confirm_linux_test.go, same package) is the fixture that already exists
// for that. It also relies on a pty's canonical mode delivering one queued
// line per read(2): confirm wraps stdin in a fresh bufio.Reader on every
// call, and a reader that could return both answers in one Read would starve
// the second call.
func TestRMAtProjectScopeAsksPerVM(t *testing.T) {
	projectRoot(t, twoVMs)
	for _, n := range []string{"myrepo-dev", "myrepo-ci"} {
		if err := (&config.VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
			t.Fatal(err)
		}
	}
	master, tty := openTestPTY(t)
	if _, err := master.Write([]byte("y\nn\n")); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := Main([]string{"rm"}, "test", tty, tty, &errOut)
	if code != ExitFail {
		t.Errorf("exit = %d, want %d after a declined VM", code, ExitFail)
	}
	if _, err := config.Load("myrepo-dev"); err == nil {
		t.Error("myrepo-dev survived a y answer")
	}
	if _, err := config.Load("myrepo-ci"); err != nil {
		t.Error("myrepo-ci was deleted after an n answer")
	}
}
