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
// qemu-system-x86_64 installed, and returns the func that stops it, which the
// caller must defer.
//
// qemu.Running reads dir/qemu.pid and then requires dir+"/" to appear in
// /proc/<pid>/cmdline. A pid alone is not enough: pids are reused, and a
// stale pidfile would otherwise report a ghost. The fake process must be
// real, alive, and carry the directory in its argv.
//
// This helper lives in one place, shared by internal/core and internal/cli,
// so the two callers cannot drift apart on how the fake process is built.
//
// Two details are load-bearing:
//
//   - `sh -c "sleep 100; :"`, not `sh -c "sleep 100"`. A simple command
//     makes sh exec it directly, replacing sh's own argv, so the directory,
//     the entire point, vanishes from the cmdline. A compound command keeps
//     sh alive as itself.
//   - The returned stop func waits after killing. Without it the child
//     becomes a zombie, and a zombie's /proc/<pid>/cmdline reads back
//     empty, so a later check reports "not running" for a pid that still
//     exists.
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

	// Waits for the exec to land before publishing the pidfile.
	//
	// cmd.Start returns once the fork is under way, not once the child has
	// exec'd. In that window /proc/<pid>/cmdline still reports the parent's
	// argv, the go test binary, which does not contain dir. A check landing
	// there sees a pid whose cmdline does not match and concludes the VM is
	// not running. qemu.Running does not just return false: it deletes the
	// pidfile it just read, so the VM stays "not running" forever after.
	//
	// Publishing the pidfile only once the cmdline actually matches removes
	// this race.
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cmd.Process.Pid))
		if err == nil && bytes.Contains(b, []byte(dir+"/")) {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("fake VM process never showed %q in its cmdline; qemu.Running would not match it", dir+"/")
		}
		time.Sleep(2 * time.Millisecond)
	}

	pidFile := filepath.Join(dir, "qemu.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}
