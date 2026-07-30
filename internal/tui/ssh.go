package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
)

// sshInto suspends the TUI and hands the terminal to a real ssh process.
// Host keys are ignored on purpose: live VMs regenerate them every boot, so
// strict checking would fail on every single start of a machine stoat built.
//
// This is the interactive path: a human is at the keyboard, so unlike
// internal/sshx (the unattended path) it does NOT set BatchMode=yes — a
// disk-mode VM with no key installed may only be reachable by typing a
// password. It does bound the connection with timeouts so a guest that
// never answers (no sshd yet, still booting) fails fast instead of hanging
// the terminal forever.
func sshInto(v *config.VM) tea.Cmd {
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
	args = append(args, "root@127.0.0.1")

	c := exec.Command("ssh", args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 {
				return statusMsg(fmt.Sprintf(
					"ssh: couldn't reach root@127.0.0.1:%d — VM may still be booting/provisioning, or a disk VM needs setup-alpine run at its console first",
					v.SSHPort))
			}
			return statusMsg("ssh: " + err.Error())
		}
		return statusMsg("")
	})
}
