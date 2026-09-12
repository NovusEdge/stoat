//go:build linux

package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// stubQEMU starts a process that stands in for a VM's QEMU: its cmdline holds
// the VM directory, so Running matches it the way it matches the real thing.
// It ignores SIGTERM when deaf is true, which is the case Stop used to report
// as success.
func stubQEMU(t *testing.T, dir string, deaf bool) int {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The loop matters. A shell given a trap and one command still execs that
	// command on some shells, which drops the trap and makes a "deaf" stub
	// die on the first SIGTERM.
	// The loop keeps the shell in place. A shell given one command execs it,
	// which both drops a trap and replaces the cmdline that Running matches
	// the VM directory against.
	//
	// The ready file is written after the trap is installed. A SIGTERM that
	// arrives before that still kills a stub meant to ignore it.
	ready := filepath.Join(dir, "ready")
	script := "while true; do sleep 0.1; done"
	if deaf {
		script = "trap '' TERM; " + script
	}
	script = "touch " + ready + "; " + script
	// The directory reaches the cmdline as a plain argument, which is what
	// Running reads; $0 for a -c script is the argument after it.
	cmd := exec.Command("/bin/sh", "-c", script, dir+"/qemu.pid")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = kill(pid) })

	// The pidfile is written only after the cmdline is readable. /proc reads
	// empty between fork and exec, and Running deletes the pidfile it cannot
	// match, so writing first would lose the record on the first poll.
	deadline := time.Now().Add(5 * time.Second)
	for {
		cmdline, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
		if err == nil && cmdlineMatches(cmdline, dir) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("stub process %d never signalled ready", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu.pid"), []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pid
}

func shortWaits(t *testing.T) {
	t.Helper()
	powerdownWait, termWait, killWait = 200*time.Millisecond, 300*time.Millisecond, 2*time.Second
	t.Cleanup(func() {
		powerdownWait, termWait, killWait = 10*time.Second, 10*time.Second, 5*time.Second
	})
}

// A QEMU that never reaps its SIGTERM has to be killed. Stop used to send the
// signal and return success while the process kept running.
func TestStopEscalatesToSIGKILL(t *testing.T) {
	shortWaits(t)
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "deaf")
	pid := stubQEMU(t, dir, true)
	v := &config.VM{Name: "deaf", Dir: dir, Mode: "live", RAM: 1024, CPUs: 1}

	// Prove the stub ignores SIGTERM. Without this the test could pass while
	// the kill path never runs.
	if err := terminate(pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if !Running(v) {
		t.Fatal("the stub died on SIGTERM, so this test never reaches SIGKILL")
	}

	if err := Stop(v); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	if alivePID(pid) {
		t.Fatal("Stop returned while the process was still running")
	}
}

// A process that honours SIGTERM never reaches the kill.
func TestStopReturnsAfterSIGTERM(t *testing.T) {
	shortWaits(t)
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "polite")
	pid := stubQEMU(t, dir, false)
	v := &config.VM{Name: "polite", Dir: dir, Mode: "live", RAM: 1024, CPUs: 1}

	if err := Stop(v); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	if alivePID(pid) {
		t.Fatal("the process outlived Stop")
	}
}

func TestStopIsANoOpForAStoppedVM(t *testing.T) {
	shortWaits(t)
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "idle")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "idle", Dir: dir, Mode: "live", RAM: 1024, CPUs: 1}
	if err := Stop(v); err != nil {
		t.Fatalf("Stop = %v, want nil for a VM that is not running", err)
	}
}

func alivePID(pid int) bool {
	for i := 0; i < 20; i++ {
		if err := terminate0(pid); err != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return true
}
