package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

type detailModel struct {
	vm  *config.VM
	log string
}

func newDetail(v *config.VM) detailModel { return detailModel{vm: v} }

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// tailLog reads the last n lines of the most recent provision run.
func tailLog(v *config.VM, n int) string {
	b, err := os.ReadFile(v.Dir + "/last-provision.log")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (m model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if m.detail.vm == nil {
			return m, nil
		}
		m.detail.log = tailLog(m.detail.vm, 10)
		if m.screen == screenDetail {
			return m, tick()
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "left", "h", "q":
			m.screen = screenList
			return m, loadVMs
		}
		if m.detail.vm == nil {
			return m, nil
		}
		switch msg.String() {
		case "e":
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, m.detail.vm.Dir+"/vm.toml")
			return m, tea.ExecProcess(c, func(err error) tea.Msg {
				v, lerr := config.Load(m.detail.vm.Name)
				if lerr != nil {
					return statusMsg(lerr.Error())
				}
				return vmReloadedMsg{v}
			})
		case "i":
			v := m.detail.vm
			if v.Mode != "disk" {
				m.status = "installed only applies to disk vms"
				return m, nil
			}
			v.Installed = !v.Installed
			if err := v.Save(); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.status = fmt.Sprintf("%s installed=%v", v.Name, v.Installed)
			return m, loadVMs
		case "s":
			if qemu.Running(m.detail.vm) {
				return m, sshInto(m.detail.vm)
			}
			m.status = "not running"
		}
	case vmReloadedMsg:
		m.detail.vm = msg.vm
		return m, loadVMs
	}
	return m, nil
}

type vmReloadedMsg struct{ vm *config.VM }

func (m model) viewDetail() string {
	v := m.detail.vm
	var b strings.Builder

	if v == nil {
		b.WriteString(dimStyle.Render("  no vm selected") + "\n\n")
		if m.status != "" {
			b.WriteString(warnStyle.Render("  "+m.status) + "\n")
		}
		b.WriteString(dimStyle.Render("  esc back") + "\n")
		return b.String()
	}

	state := downStyle.Render("stopped")
	if qemu.Running(v) {
		state = upStyle.Render("running")
	}
	b.WriteString(accentStyle.Render("  "+v.Name) + dimStyle.Render(" · "+v.Mode+" · ") + state + "\n\n")

	line := func(k, val string) {
		b.WriteString(fmt.Sprintf("  %-9s %s\n", dimStyle.Render(k), val))
	}
	line("iso", v.ISO)
	if v.Mode == "disk" {
		size := "—"
		if fi, err := os.Stat(v.DiskPath()); err == nil {
			size = fmt.Sprintf("%.1fG on disk", float64(fi.Size())/(1<<30))
		}
		line("disk", v.Disk+"  "+size)
		installed := warnStyle.Render("no — run setup-alpine in the qemu window, then press i")
		if v.Installed {
			installed = upStyle.Render("yes")
		}
		line("installed", installed)
	}
	line("ssh", fmt.Sprintf("root@127.0.0.1:%d", v.SSHPort))
	if v.Share != "" {
		line("share", v.Share+dimStyle.Render(" → /mnt/host"))
	}
	if len(v.Recipes) > 0 {
		line("recipes", strings.Join(v.Recipes, ", "))
	}

	if m.detail.log != "" {
		b.WriteString("\n" + dimStyle.Render("  last provision") + "\n")
		for _, l := range strings.Split(m.detail.log, "\n") {
			b.WriteString("  " + dimStyle.Render(l) + "\n")
		}
	}

	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(warnStyle.Render("  "+m.status) + "\n")
	}
	b.WriteString(dimStyle.Render("  e edit   i installed   s ssh   esc back") + "\n")
	return b.String()
}
