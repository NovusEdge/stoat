package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/sshx"
)

// reachabilityProbe is how long a failed interactive ssh attempt waits to
// re-check sshd's readiness before reporting status, so the message can
// distinguish "still booting" from a real failure. Short: this only runs
// after ssh has already failed once, so it just needs to catch the common
// case of a still-booting guest without making the user wait long for the
// status line.
const reachabilityProbe = 800 * time.Millisecond

// sshInto suspends the TUI and hands the terminal to a real ssh process.
// Host keys are ignored on purpose: live VMs regenerate them every boot, so
// strict checking would fail on every single start of a machine stoat built.
//
// This is the interactive path: a human is at the keyboard, so unlike
// internal/sshx (the unattended path) it does NOT set BatchMode=yes, since a
// disk-mode VM with no key installed may only be reachable by typing a
// password. It does bound the connection with timeouts so a guest that
// never answers (no sshd yet, still booting) fails fast instead of hanging
// the terminal forever.
func sshInto(v *config.VM) tea.Cmd {
	c := exec.Command("ssh", sshIntoArgs(v)...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 {
				// ssh itself failed to connect. Reuse core's banner-aware
				// readiness check (the same signal Apply/Provision wait on)
				// instead of guessing from the exit code alone, so the
				// message can tell a guest that's still booting apart from
				// one that's genuinely unreachable (e.g. a disk VM with no
				// key installed and no sshd ever going to answer).
				// No cancellation source reaches here: this runs after ssh
				// has already exited, in a plain tea.ExecProcess callback.
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
func sshIntoArgs(v *config.VM) []string {
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
	return append(args, sshx.User(v)+"@127.0.0.1")
}

// unreachableMsg is what sshInto reports when ssh itself fails to connect
// (exit 255) and the guest is not still booting. It names the actual
// installer for v's OS (installerName, internal/tui/provision.go) instead
// of hardcoding "setup-alpine", which was wrong on every non-Alpine guest.
func unreachableMsg(v *config.VM) string {
	return fmt.Sprintf(
		"ssh: couldn't reach %s@127.0.0.1:%d, a disk VM needs %s run at its console first, or the key isn't installed",
		sshx.User(v), v.SSHPort, installerName(v))
}
