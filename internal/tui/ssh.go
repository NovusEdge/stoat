package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/sshx"
)

// reachabilityProbe is how long a failed interactive ssh attempt waits
// before it re-checks sshd's readiness. The re-check lets the status
// message tell "still booting" apart from a real failure.
//
// The wait stays short. It only runs after ssh has already failed once, to
// catch the common case of a still-booting guest without a long wait on the
// status line.
const reachabilityProbe = 800 * time.Millisecond

// sshInto suspends the TUI and hands the terminal to a real ssh process.
//
// Host keys are ignored on purpose. A live VM regenerates them every boot,
// so strict checking would fail on every start of a machine stoat built.
//
// A human is at the keyboard here, unlike internal/sshx, the unattended
// path. sshInto does not set BatchMode=yes: a disk VM with no key installed
// may only be reachable by typing a password. Connection timeouts still
// apply, so a guest that never answers (no sshd yet, still booting) fails
// fast instead of hanging the terminal.
func sshInto(v core.VM) tea.Cmd {
	c := exec.Command("ssh", sshIntoArgs(v)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 {
				// ssh itself failed to connect. Reuse core's banner-aware readiness check
				// (the same signal Apply/Provision wait on) instead of guessing from the
				// exit code alone. The message then tells a guest that is still booting
				// apart from one that is genuinely unreachable (a disk VM with no key
				// installed and no sshd ever going to answer).
				//
				// No cancellation source reaches here. This runs after ssh has already
				// exited, inside a plain tea.ExecProcess callback.
				ctx, cancel := context.WithTimeout(context.Background(), reachabilityProbe)
				defer cancel()
				if core.Wait(ctx, v.Name, core.UntilReachable) != nil {
					return errMsg(fmt.Sprintf(
						"%s: still booting, sshd not reachable yet on port %d, try again shortly",
						v.Name, v.SSHPort))
				}
				return errMsg(unreachableMsg(v))
			}
			return errMsg("ssh: " + err.Error())
		}
		return statusMsg("")
	})
}

// sshIntoArgs is the argv (excluding argv[0]) for the interactive ssh
// process. Split out from sshInto so the target user can be asserted on
// directly without driving a tea.ExecProcess.
func sshIntoArgs(v core.VM) []string {
	key := keys.PrivatePath()
	args := []string{
		"-p", fmt.Sprint(v.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=3",
	}
	if _, err := os.Stat(key); err == nil {
		args = append(args, "-i", key)
	}
	return append(args, sshx.User(cfgVM(v))+"@127.0.0.1")
}

// unreachableMsg is what sshInto reports when ssh fails to connect (exit
// 255) and the guest is not still booting.
//
// An apkovl-backend (Alpine) disk VM installs itself and is unreachable until
// it reboots. Any other backend needs its installer (installerName) run at the
// console first.
func unreachableMsg(v core.VM) string {
	if v.Backend == "apkovl" {
		return fmt.Sprintf(
			"ssh: couldn't reach %s@127.0.0.1:%d, a disk VM isn't reachable until it finishes installing and reboots, or the key isn't installed",
			sshx.User(cfgVM(v)), v.SSHPort)
	}
	return fmt.Sprintf(
		"ssh: couldn't reach %s@127.0.0.1:%d, a disk VM needs %s run at its console first, or the key isn't installed",
		sshx.User(cfgVM(v)), v.SSHPort, installerName(v.OS))
}
