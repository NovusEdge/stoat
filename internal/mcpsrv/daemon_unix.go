//go:build !windows

package mcpsrv

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session, so a Ctrl-C in the shell that ran
// `mcp up` does not reach the server.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// alive reports whether the pid names a live process. Signal 0 performs the
// permission and existence checks without delivering anything.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
