// Package testutil holds test helpers shared across stoat's packages. It is
// imported only by _test files; nothing in the shipping binary depends on it.
package testutil

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// FakeRunning makes the VM at dir look running to qemu.Running, without
// qemu-system-x86_64 installed, and returns the func that stops it — which the
// caller must defer.
//
// qemu.Running reads dir/qemu.pid and then requires dir+"/" to appear in
// /proc/<pid>/cmdline (a pid alone is not enough: pids are reused, and a stale
// pidfile would otherwise report a ghost). So the fake process has to be real,
// alive, and carry the directory in its argv.
//
// THIS LIVES IN ONE PLACE ON PURPOSE. It previously existed as two
// independently-maintained copies, in internal/core and internal/cli. Both
// spawned `sleep 100 <dir>/marker` — which does not work, because sleep sums
// its arguments as durations and rejects a non-numeric one, so the process
// exited about a millisecond in and the "running" VM was dead before anything
// looked. When that was found, only one copy was fixed. The other went on
// backing a test asserting that `rm` REFUSES to delete a running VM, and
// failed roughly 40% of the time under concurrency while passing in isolation
// — a test that intermittently proved the opposite of its name, over a
// destructive operation.
//
// Two details are load-bearing:
//
//   - `sh -c "sleep 100; :"`, not `sh -c "sleep 100"`. A SIMPLE command makes
//     sh exec it directly, replacing sh's own argv — and the directory, the
//     entire point, vanishes from the cmdline. A compound command keeps sh
//     alive as itself.
//   - The returned stop func Waits after killing. Without it the child becomes
//     a zombie, and a zombie's /proc/<pid>/cmdline reads back EMPTY, so a
//     later check sees "not running" for a pid that still exists.
func FakeRunning(t *testing.T, dir string) func() {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker")
	cmd := exec.Command("sh", "-c", "sleep 100; :", marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// WAIT FOR THE EXEC TO LAND before publishing the pidfile.
	//
	// cmd.Start returns once the fork is under way, not once the child has
	// exec'd. In that window /proc/<pid>/cmdline still reports the PARENT's
	// argv — the go test binary — which does not contain dir. A check landing
	// there sees a pid whose cmdline does not match and concludes the VM is
	// not running, and qemu.Running does not merely return false: it DELETES
	// the pidfile it just read, so the VM stays "not running" forever after.
	//
	// Locally the exec wins that race essentially always, which is why this
	// passed here and failed on a loaded CI runner. Publishing the pidfile
	// only once the cmdline actually matches removes the window entirely.
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
		if err == nil && bytes.Contains(b, []byte(dir+"/")) {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatalf("fake VM process never showed %q in its cmdline; qemu.Running would not match it", dir+"/")
		}
		time.Sleep(2 * time.Millisecond)
	}

	pidFile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal(err)
	}
	return func() {
		cmd.Process.Kill()
		cmd.Wait()
	}
}
