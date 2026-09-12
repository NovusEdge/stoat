//go:build linux

package qemu

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/novusedge/stoat/internal/config"
)

func pid(v *config.VM) int {
	b, err := os.ReadFile(v.PidPath())
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return p
}

// Running reports whether this VM's QEMU process is alive. The cmdline check
// matters: pids are reused, and a stale pidfile would otherwise report a ghost.
func Running(v *config.VM) bool {
	p := pid(v)
	if p == 0 {
		return false
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p))
	if err != nil {
		_ = os.Remove(v.PidPath())
		return false
	}
	if !cmdlineMatches(cmdline, v.Dir) {
		_ = os.Remove(v.PidPath())
		return false
	}
	return true
}

// StartedAt returns when the VM's QEMU process started (the pidfile's mtime),
// or the zero time if it is stopped.
func StartedAt(v *config.VM) time.Time {
	if !Running(v) {
		return time.Time{}
	}
	fi, err := os.Stat(v.PidPath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func terminate(p int) error { return syscall.Kill(p, syscall.SIGTERM) }

func kill(p int) error { return syscall.Kill(p, syscall.SIGKILL) }

// terminate0 delivers no signal and performs the existence check only.
func terminate0(p int) error { return syscall.Kill(p, 0) }

// waitExit blocks until p exits or within elapses. The second return is false
// when this kernel has no pidfd, and the caller polls instead.
//
// A pidfd becomes readable when its process dies, so this wakes at the exit
// rather than at the next tick of a poll loop. pidfd_open landed in Linux 5.3;
// an older kernel answers ENOSYS.
func waitExit(p int, within time.Duration) (exited bool, supported bool) {
	fd, err := unix.PidfdOpen(p, 0)
	if err != nil {
		if errors.Is(err, unix.ENOSYS) {
			return false, false
		}
		// ESRCH means the process is already gone, which is the answer the
		// caller wants. Any other error (EPERM on a process owned by someone
		// else) is not this function's to interpret.
		return errors.Is(err, unix.ESRCH), true
	}
	defer func() { _ = unix.Close(fd) }()

	deadline := time.Now().Add(within)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return false, true
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(left.Milliseconds()))
		if errors.Is(err, unix.EINTR) {
			// A signal arriving mid-wait is not an answer; resume on what is
			// left of the deadline.
			continue
		}
		if err != nil {
			return false, false
		}
		if n > 0 {
			return true, true
		}
	}
}
