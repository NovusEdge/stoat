//go:build linux

package qemu

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

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
