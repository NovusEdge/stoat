//go:build linux

package qemu

import (
	"path/filepath"
	"testing"
	"time"
)

// waitExit must return when the process dies, not when the caller's deadline
// expires. The generous window is the test: a poll loop that ignored the exit
// would take all of it.
func TestWaitExitWakesAtTheExit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	pid := stubQEMU(t, filepath.Join(root, "vm"), false)

	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		exited, supported := waitExit(pid, 30*time.Second)
		if !exited || !supported {
			done <- -1
			return
		}
		done <- time.Since(start)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := kill(pid); err != nil {
		t.Fatal(err)
	}

	select {
	case took := <-done:
		switch {
		case took < 0:
			t.Fatal("waitExit reported no exit for a process that was killed")
		case took > 5*time.Second:
			t.Errorf("waitExit took %s, which is a poll to the deadline rather than a wake at the exit", took)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waitExit never returned")
	}
}

// A process that outlives the window reports that it is still there.
func TestWaitExitHonoursTheDeadline(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	pid := stubQEMU(t, filepath.Join(root, "vm"), false)

	start := time.Now()
	exited, supported := waitExit(pid, 200*time.Millisecond)
	if !supported {
		t.Skip("this kernel has no pidfd_open")
	}
	if exited {
		t.Fatal("waitExit reported an exit for a living process")
	}
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Errorf("waitExit returned after %s, well before its 200ms window", took)
	}
}

// A pid that is already gone is an exit, not an error.
func TestWaitExitOnADeadProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	pid := stubQEMU(t, filepath.Join(root, "vm"), false)
	if err := kill(pid); err != nil {
		t.Fatal(err)
	}
	// The kernel keeps a zombie until it is reaped, and stubQEMU's own Wait
	// does that; poll until the pid is genuinely gone.
	deadline := time.Now().Add(5 * time.Second)
	for terminate0(pid) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the killed stub was never reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}

	exited, supported := waitExit(pid, time.Second)
	if !supported {
		t.Skip("this kernel has no pidfd_open")
	}
	if !exited {
		t.Error("waitExit reported a live process for a pid that is gone")
	}
}
