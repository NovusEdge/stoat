//go:build linux

package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/testutil"
	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("cannot query pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("cannot unlock pseudo-terminal: %v", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open pseudo-terminal slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func TestRecipeURLPreviewUsesManifestFieldsOnATTY(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := testutil.GitRepo(t, map[string]string{
		"recipe.toml": "schema = 3\nname = \"demo\"\ndescription = \"demo recipe\"\nos = [\"alpine\"]\nrequires = [\"git\"]\nscript = \"install.sh\"\n\n[params.channel]\ntype = \"enum\"\nvalues = [\"stable\", \"test\"]\ndefault = \"stable\"\n",
		"install.sh":  "#!/bin/sh\necho demo\n",
	})
	bare := filepath.Join(filepath.Dir(src), "demo.git")
	if err := os.Rename(src, bare); err != nil {
		t.Fatal(err)
	}
	master, tty := openTestPTY(t)
	if _, err := master.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := Main([]string{"recipe", "add", bare, "--global"}, "test", tty, tty, &errOut)
	if code != ExitOK {
		t.Fatalf("TTY URL add exit = %d, stderr = %q", code, errOut.String())
	}
	if err := master.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	output := string(buf[:n])
	for _, want := range []string{"demo", "alpine", "git", "channel"} {
		if !strings.Contains(output, want) {
			t.Errorf("TTY preview missing %q: %q", want, output)
		}
	}
}
