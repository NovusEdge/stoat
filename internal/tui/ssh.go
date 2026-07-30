package tui

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
)

// sshInto suspends the TUI and hands the terminal to a real ssh process.
// Host keys are ignored on purpose: live VMs regenerate them every boot, so
// strict checking would fail on every single start of a machine stoat built.
func sshInto(v *config.VM) tea.Cmd {
	key := config.Root() + "/id_stoat"
	args := []string{
		"-p", fmt.Sprint(v.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
	if _, err := os.Stat(key); err == nil {
		args = append(args, "-i", key)
	}
	args = append(args, "root@127.0.0.1")

	c := exec.Command("ssh", args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return statusMsg("ssh: " + err.Error())
		}
		return statusMsg("")
	})
}
