package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

type detailModel struct {
	vm  *config.VM
	log string
}

func newDetail(v *config.VM) detailModel { return detailModel{vm: v} }

// tickMsg carries the generation of the detail-screen visit that scheduled
// it. updateDetail only re-arms the chain when gen still matches
// m.detailGen; every other visit to the detail screen bumps detailGen, so a
// stale chain left over from a prior visit dies silently on its next tick
// instead of re-arming and running forever alongside the live one.
type tickMsg struct {
	t   time.Time
	gen int
}

func tick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{t: t, gen: gen} })
}

// tailLog reads the last n lines of the most recent provision run.
func tailLog(v *config.VM, n int) string {
	b, err := os.ReadFile(filepath.Join(v.Dir, "last-provision.log"))
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
		if msg.gen != m.detailGen {
			// A stale chain from a previous visit to the detail screen.
			// Let it die here instead of re-arming.
			return m, nil
		}
		if m.detail.vm == nil {
			return m, nil
		}
		m.detail.log = tailLog(m.detail.vm, 10)
		if m.screen == screenDetail {
			return m, tick(m.detailGen)
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "left", "h", "q":
			m.screen = screenList
			m.showHelp = false
			return m, loadVMs
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
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
			c := exec.Command(editor, filepath.Join(m.detail.vm.Dir, "vm.toml"))
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
			// Save a copy first: only flip the live in-memory VM once the
			// write to disk actually succeeds, so a failed Save can't leave
			// the pane showing a state that was never persisted.
			next := *v
			next.Installed = !v.Installed
			if err := next.Save(); err != nil {
				m.status = err.Error()
				return m, nil
			}
			v.Installed = next.Installed
			m.status = fmt.Sprintf("%s installed=%v", v.Name, v.Installed)
			return m, loadVMs
		case "s":
			if qemu.Running(m.detail.vm) {
				return m, sshInto(m.detail.vm)
			}
			m.status = "not running"
		case "p":
			return m, m.startProvision(m.detail.vm)
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

	if v == nil {
		parts := []string{pane("", dimStyle.Render("no vm selected"), m.width), ""}
		if m.status != "" {
			parts = append(parts, warnStyle.Render(m.status))
		}
		parts = append(parts, dimStyle.Render("esc back"))
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}

	state := downStyle.Render("stopped")
	if qemu.Running(v) {
		state = upStyle.Render("running")
	}

	var facts strings.Builder
	facts.WriteString(dimStyle.Render(v.Mode) + dimStyle.Render(" · ") + state + "\n\n")

	line := func(k, val string) {
		fmt.Fprintf(&facts, "%s %s\n", dimStyle.Render(fmt.Sprintf("%-9s", k)), val)
	}
	line("iso", v.ISO)
	if v.Mode == "disk" {
		size := "—"
		if fi, err := os.Stat(v.DiskPath()); err == nil {
			size = fmt.Sprintf("%.1fG on disk", float64(fi.Size())/(1<<30))
		}
		line("disk", v.Disk+"  "+size)
		// A disk VM is only an Alpine one when its image says so; a BYO
		// Fedora/Debian/unknown ISO has no setup-alpine to run, and telling
		// the user to run one is worse than saying nothing specific.
		installer := "the installer"
		if v.OS == "alpine" {
			installer = "setup-alpine"
		}
		installed := warnStyle.Render("no — run " + installer + " in the qemu window, then press i")
		if v.Installed {
			installed = upStyle.Render("yes")
		}
		line("installed", installed)
	}
	sshUser := v.SSHUser
	if sshUser == "" {
		sshUser = "root"
	}
	line("ssh", fmt.Sprintf("%s@127.0.0.1:%d", sshUser, v.SSHPort))
	if v.Share != "" {
		line("share", v.Share+dimStyle.Render(" → /mnt/host"))
	}
	if len(v.Recipes) > 0 {
		line("recipes", strings.Join(v.Recipes, ", "))
	}

	factsBox := pane(v.Name, strings.TrimRight(facts.String(), "\n"), m.width)

	parts := []string{factsBox}
	if m.detail.log != "" {
		var log strings.Builder
		for i, l := range strings.Split(m.detail.log, "\n") {
			if i > 0 {
				log.WriteString("\n")
			}
			log.WriteString(dimStyle.Render(l))
		}
		parts = append(parts, "", pane("last provision", log.String(), m.width))
	}

	parts = append(parts, "")
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	parts = append(parts, renderFooter(detailHelp{sshAvailable: qemu.Running(v)}, m.width, m.showHelp))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
