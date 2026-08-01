package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	case tea.KeyPressMsg:
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
			m.edit = newEdit(m.detail.vm)
			m.screen = screenEdit
			m.showHelp = false
			m.status = ""
			return m, nil
		case "E":
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, filepath.Join(m.detail.vm.Dir, "vm.toml"))
			return m, tea.ExecProcess(c, func(err error) tea.Msg {
				v, lerr := config.Load(m.detail.vm.Name)
				if lerr != nil {
					return errMsg(lerr.Error())
				}
				return vmReloadedMsg{v}
			})
		case "i":
			v := m.detail.vm
			if v.Mode != "disk" {
				cmd := m.showToast("installed only applies to disk vms", true)
				return m, cmd
			}
			// Save a copy first: only flip the live in-memory VM once the
			// write to disk actually succeeds, so a failed Save can't leave
			// the pane showing a state that was never persisted.
			next := *v
			next.Installed = !v.Installed
			if err := next.Save(); err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			v.Installed = next.Installed
			cmd := m.showToast(fmt.Sprintf("%s installed=%v", v.Name, v.Installed), false)
			return m, tea.Batch(loadVMs, cmd)
		case "s":
			if qemu.Running(m.detail.vm) {
				return m, sshInto(m.detail.vm)
			}
			cmd := m.showToast("not running", true)
			return m, cmd
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
		return lipgloss.JoinVertical(lipgloss.Center, parts...)
	}

	state := downStyle.Render("stopped")
	if qemu.Running(v) {
		state = upStyle.Render("running")
	}

	var facts fields
	facts.row("", "", dimStyle.Render(modeLabel(v.Mode))+dimStyle.Render(glyphSep)+state)
	facts.hint(modeHint(v.Mode))
	facts.gap()

	line := func(k, val string) { facts.row("", k, val) }
	// A cloud VM has no ISO — it boots an overlay of a base image — so the
	// row would otherwise render as an empty label.
	if v.ISO != "" {
		line("iso", v.ISO)
	} else if v.Base != "" {
		line("image", filepath.Base(v.Base))
	}
	if v.Mode == "disk" {
		size := "—"
		if fi, err := os.Stat(v.DiskPath()); err == nil {
			size = fmt.Sprintf("%.1fG on disk", float64(fi.Size())/(1<<30))
		}
		line("disk", v.Disk+"  "+size)
		// Until this is true a disk VM has no sshd and no key (apkovl is
		// built for live mode only), so "p" cannot work — startProvision
		// refuses with the same instruction rather than timing out on ssh.
		installed := warnStyle.Render("no — run " + installerName(v) + " in the qemu window, then press i")
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
	// How to get in. The console line matters most for cloud images: they
	// lock every account, so without a password the qemu window shows a
	// login prompt with no valid answer — and the moment you need it is the
	// moment ssh isn't working, which is the worst time to go looking.
	if v.ConsolePassword != "" {
		line("console", sshUser+" / "+v.ConsolePassword+dimStyle.Render("  (qemu window only)"))
	} else if v.Mode == "cloud" {
		line("console", warnStyle.Render("no password set — console login is not possible"))
	}
	if v.Share != "" {
		line("share", v.Share+dimStyle.Render(" "+glyphTo+" /mnt/host"))
	}
	if len(v.Recipes) > 0 {
		names := make([]string, len(v.Recipes))
		for i, r := range v.Recipes {
			names[i] = recipeLabel(r)
		}
		line("recipes", strings.Join(names, ", "))
	}

	factsBox := pane(v.Name, facts.String(), m.width)

	parts := []string{factsBox}
	if m.detail.log != "" {
		// lipgloss re-applies the style on every line of a multi-line string,
		// so the log needs no per-line loop of its own.
		parts = append(parts, "", pane("last provision", dimStyle.Render(m.detail.log), m.width))
	}

	parts = append(parts, "")
	for _, l := range provLines(m) {
		parts = append(parts, l)
	}
	if m.status != "" {
		parts = append(parts, warnStyle.Render(m.status))
	}
	parts = append(parts, renderFooter(detailHelp{sshAvailable: qemu.Running(v)}, m.width, m.showHelp))
	// Center for the same reason as the list: the footer is wider than the
	// pane, and a left join would pin the pane to the footer's left edge.
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}
