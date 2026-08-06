package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

type detailModel struct {
	vm  core.VM
	log string
	// pager is non-nil only while the console log viewer (key L) is open. It
	// takes over the detail screen's keyboard and body; see updateDetail and
	// viewDetail.
	pager *logPager
	// snapshots is non-nil only while the snapshots modal (key S) is open. It
	// takes over the detail screen's keyboard and body the same way pager
	// does; see snapshots.go.
	snapshots *snapshotsModal
}

func newDetail(v core.VM) detailModel { return detailModel{vm: v} }

// name is the VM's identity: its DIRECTORY, never the vm.toml `name` field,
// which the "E" key lets a user edit into something else entirely. See
// core.VM.Name's comment for the bug that rule exists to prevent. core.VM.Name
// already reports that directory, so this is a pass-through kept so every
// call site below reads "the VM's identity" rather than a raw field access.
func (d detailModel) name() string { return d.vm.Name }

// coreVM re-asks core for the current view of the VM this screen is showing,
// fresh as of right now. d.vm is a snapshot taken when this screen was last
// entered or reloaded; it can go stale before the user acts on it (another
// terminal started or stopped the VM, an "s" or "p" press must not act on
// that stale State). Asking by name, rather than trusting the snapshot, is
// what keeps those two keys honest about what's true at the moment they run.
func (d detailModel) coreVM() (core.VM, error) { return core.Get(d.name()) }

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

// tailLog reads the last n lines of the most recent recipe-apply run,
// through core.Logs rather than opening ProvisionLogPath directly: Logs
// already resolves name to the right file (and hands back an empty reader
// instead of an error for a VM that has never had recipes applied, or one
// whose vm.toml is broken), so this keeps doing exactly the tail-N-lines
// work it always did, just fed from core instead of a *config.VM.
func tailLog(name string, n int) string {
	r, err := core.Logs(name, core.WhichApply)
	if err != nil {
		return ""
	}
	defer r.Close()
	b, err := io.ReadAll(r)
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
	// The pager owns the keyboard while it's open: esc closes it here,
	// before any of the detail screen's own bindings (also esc/e/p/…) get a
	// look at the message, and everything else goes to the viewport.
	if m.detail.pager != nil {
		if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "esc" {
			m.detail.pager = nil
			return m, nil
		}
		cmd := m.detail.pager.update(msg)
		return m, cmd
	}

	// The snapshots modal owns the keyboard while it's open, exactly like
	// the pager above: every message goes to it first, and it reports
	// whether it should close rather than reacting to a bare esc here, since
	// esc means different things inside it (cancel a sub-prompt vs. close
	// outright; see snapshotsModal.update).
	if m.detail.snapshots != nil {
		cmd, closed := m.detail.snapshots.update(msg)
		if closed {
			m.detail.snapshots = nil
		}
		return m, cmd
	}

	switch msg := msg.(type) {
	case logOpenedMsg:
		m.detail.pager = msg.pager
		return m, nil
	case snapshotsOpenedMsg:
		if msg.err != nil {
			cmd := m.showToast(msg.err.Error(), true)
			return m, cmd
		}
		m.detail.snapshots = newSnapshotsModal(msg.name, msg.snaps)
		return m, nil
	case tickMsg:
		if msg.gen != m.detailGen {
			// A stale chain from a previous visit to the detail screen.
			// Let it die here instead of re-arming.
			return m, nil
		}
		if m.detail.vm.Name == "" {
			return m, nil
		}
		m.detail.log = tailLog(m.detail.vm.Name, 10)
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
		if m.detail.vm.Name == "" {
			return m, nil
		}
		switch msg.String() {
		case "e":
			// The edit screen still works off the on-disk record (it writes
			// vm.toml directly through core.Update), so it needs the real
			// config.VM this screen no longer holds. By directory, per the
			// identity rule above.
			cv, err := config.Load(m.detail.name())
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			m.edit = newEdit(cv)
			m.screen = screenEdit
			m.showHelp = false
			m.status = ""
			return m, nil
		case "E":
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			c := exec.Command(editor, filepath.Join(m.detail.vm.Paths.Dir, "vm.toml"))
			return m, tea.ExecProcess(c, func(err error) tea.Msg {
				// By directory. This used to reload m.detail.vm.Name, the
				// vm.toml field, which is the one thing the editor session
				// that just ran is able to change: renaming a VM in the file
				// made the reload miss, or hit a different VM entirely.
				v, lerr := m.detail.coreVM()
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
			// core.Update takes the data-root lock and writes vm.toml itself;
			// this used to call a second, unlocked Save() straight onto the
			// file, bypassing the same lock every other mutation goes
			// through. Only adopt the toggle in memory once the write to
			// disk actually lands, so a failed Update can't leave the pane
			// showing a state that was never persisted.
			next := !v.Installed
			updated, err := core.Update(v.Name, core.Patch{Installed: &next})
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			m.detail.vm = updated
			cmd := m.showToast(fmt.Sprintf("%s installed=%v", updated.Name, updated.Installed), false)
			return m, tea.Batch(loadVMs, cmd)
		case "s":
			v, err := m.detail.coreVM()
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			if v.State == core.StateRunning {
				return m, sshInto(v)
			}
			cmd := m.showToast("not running", true)
			return m, cmd
		case "p":
			v, err := m.detail.coreVM()
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			return m, m.startProvision(v)
		case "L":
			return m, openLogPager(m.detail.name())
		case "S":
			return m, openSnapshots(m.detail.name())
		case "t":
			v := m.detail.vm
			if !consolePasswordAvailable(v) {
				cmd := m.showToast("no console password to type", true)
				return m, cmd
			}
			return m, typeConsolePassword(v)
		case "c":
			v := m.detail.vm
			if !consolePasswordAvailable(v) {
				cmd := m.showToast("no console password to copy", true)
				return m, cmd
			}
			// tea.SetClipboard uses OSC 52: no xclip/wl-copy dependency, no
			// X11-vs-Wayland branch, and it works over ssh. Never toast the
			// password itself, just confirm it moved.
			copied := tea.SetClipboard(v.ConsolePassword)
			toasted := m.showToast(v.Name+": console password copied to clipboard", false)
			return m, tea.Batch(copied, toasted)
		}
	case vmReloadedMsg:
		m.detail.vm = msg.vm
		return m, loadVMs
	}
	return m, nil
}

type vmReloadedMsg struct{ vm core.VM }

// consolePasswordAvailable reports whether v has a console password that
// could be typed or copied right now: the VM must be running (the monitor
// socket and the login prompt both only exist then), and it must actually
// have one set. This is the same rule the "console" row in viewDetail and
// qemu.consoleCredential's log line already use. v.State is core.Get's own
// point-in-time answer to "is qemu running", so this needs no separate
// qemu.Running call of its own.
func consolePasswordAvailable(v core.VM) bool {
	return v.State == core.StateRunning && v.ConsolePassword != ""
}

// qemuMonitorVM builds the minimal *config.VM qemu.TypeConsolePassword needs
// to dial v's monitor socket: MonitorPath is a pure filepath.Join off Dir, so
// Dir plus the password is everything that call requires, without loading a
// second copy of the whole on-disk record. Same shim app.go's cfgVM uses to
// cross the same boundary for sshx.
func qemuMonitorVM(v core.VM) *config.VM {
	return &config.VM{Name: v.Name, Dir: v.Paths.Dir, ConsolePassword: v.ConsolePassword}
}

// typeConsolePassword has qemu type v's console password into the guest, off
// the UI goroutine: sending it can take a while (one monitor round trip per
// character, deliberately paced (see qemu.TypeConsolePassword), and the TUI
// must stay responsive while that happens.
func typeConsolePassword(v core.VM) tea.Cmd {
	return func() tea.Msg {
		if err := qemu.TypeConsolePassword(qemuMonitorVM(v)); err != nil {
			return errMsg(err.Error())
		}
		return statusMsg(v.Name + ": typed console password into the guest")
	}
}

func (m model) viewDetail() string {
	v := m.detail.vm

	if v.Name == "" {
		parts := []string{pane("", dimStyle.Render("no vm selected"), m.width), ""}
		if m.status != "" {
			parts = append(parts, warnStyle.Render(m.status))
		}
		parts = append(parts, dimStyle.Render("esc back"))
		return lipgloss.JoinVertical(lipgloss.Center, parts...)
	}

	if m.detail.pager != nil {
		m.detail.pager.resize(m.width, m.height)
		return lipgloss.JoinVertical(lipgloss.Center, m.detail.pager.view(m.width))
	}

	if m.detail.snapshots != nil {
		m.detail.snapshots.resize(m.width, m.height)
		return lipgloss.JoinVertical(lipgloss.Center, m.detail.snapshots.view(m.width))
	}

	state := downStyle.Render("stopped")
	if v.State == core.StateRunning {
		state = upStyle.Render("running")
	}

	var facts fields
	facts.row("", "", dimStyle.Render(modeLabel(v.Mode))+dimStyle.Render(glyphSep)+state)
	facts.hint(modeHint(v.Mode))
	facts.gap()

	line := func(k, val string) { facts.row("", k, val) }
	// A cloud VM has no ISO. It boots an overlay of a base image instead, so
	// the row would otherwise render as an empty label.
	if v.ISO != "" {
		line("iso", v.ISO)
	} else if v.Base != "" {
		line("image", filepath.Base(v.Base))
	}
	if v.Mode == "disk" {
		size := "-"
		if fi, err := os.Stat(v.Paths.Disk); err == nil {
			size = fmt.Sprintf("%.1fG on disk", float64(fi.Size())/(1<<30))
		}
		line("disk", v.Disk+"  "+size)
		// Until this is true the VM boots its installer ISO, so "p" would be
		// provisioning the installer's tmpfs. startProvision refuses with
		// the same instruction rather than timing out on ssh. The next start
		// sets it automatically once the install has written to the disk; "i"
		// is the override for when that guess is wrong either way.
		installed := warnStyle.Render("no: run " + installerName(v.OS) + " in the qemu window, then restart")
		if v.Installed {
			installed = upStyle.Render("yes")
		}
		line("installed", installed)
	}
	sshUser := sshx.User(cfgVM(v))
	line("ssh", fmt.Sprintf("%s@127.0.0.1:%d", sshUser, v.SSHPort))
	// Forwards are otherwise invisible everywhere in the TUI (see the
	// migration plan's D1): showing them here is what lets a user notice
	// "oh, another VM already has that port" before the edit screen lets
	// them collide with it. Omitted entirely when there are none, rather
	// than a "forwards: none" row, matching every other optional row above.
	if len(v.Forwards) > 0 {
		label := "forward"
		for _, f := range v.Forwards {
			line(label, fmt.Sprintf("%d %s %d (guest)", f.HostPort, glyphTo, f.GuestPort))
			label = ""
		}
		// qemu's user-mode netdev argv is fixed at process launch; it has no
		// way to hot-add a hostfwd rule to a running process. So a forward
		// declared while the VM is running is saved to vm.toml (and WILL
		// apply) but has no effect on the qemu process already up. This is
		// exactly the distinction docs/design/core-api.md §8 decision 5
		// exists to keep visible: "applied later" must never read the same
		// as "already true". Rendered as its own line, in its own color, so
		// it cannot be mistaken for a caption on the rows above it.
		effect := upStyle.Render("in effect")
		if v.State == core.StateRunning {
			effect = warnStyle.Render("saved, applies at next start (not the running process)")
		}
		facts.row("", "", effect)
	}
	// qemu.DisplayKind's own comment names this exact case: it is stated over
	// the three facts it depends on (mode, installed, host graphical) rather
	// than over a *config.VM, precisely so a core.VM caller like this one can
	// ask it directly instead of going through qemu.NeedsWindow/WantsWindow,
	// which only exist for *config.VM callers.
	//
	// "connect a VNC viewer here" was the whole hint and it was not enough:
	// the reported failure is a disk VM whose window disappears the moment
	// setup-alpine marks it installed, by a user who then has a path and no
	// idea what opens it. So print the command, for a viewer that is actually
	// on this host.
	//
	// The host is the second half of the rule: with no graphical session, even
	// the install console lands on this socket, and that is the case where a
	// user most needs to be told, since the alternative was a VM that refused
	// to start at all.
	if graphical := qemu.GraphicalSession(); qemu.DisplayKind(v.Mode, v.Installed, graphical) != qemu.DisplayWindow {
		if !graphical && qemu.DisplayKind(v.Mode, v.Installed, true) == qemu.DisplayWindow {
			facts.row("", "", warnStyle.Render("no usable graphical session on this host: the installer console is on vnc"))
		}
		line("vnc", v.Paths.VNCSocket)
		att := qemu.AttachVNC(v.Paths.VNCSocket)
		if att.Command == "" {
			facts.row("", "", warnStyle.Render("no VNC viewer found: install "+strings.Join(att.Missing, " or ")))
		} else {
			facts.row("", "", dimStyle.Render(att.Command))
			if att.Then != "" {
				facts.row("", "", dimStyle.Render(att.Then))
			}
		}
	}
	// How to get in. The console line matters most for cloud images: they
	// lock every account, so without a password the console shows a login
	// prompt with no valid answer, and the moment you need it is the moment
	// ssh isn't working, which is the worst time to go looking. This
	// password is only ever set for the cloudinit backend (form.go), which
	// is always cloud mode, so it is always reached over VNC, never a qemu
	// window.
	if v.ConsolePassword != "" {
		line("console", sshUser+" / "+v.ConsolePassword+dimStyle.Render("  (over vnc, above)"))
	} else if v.Mode == "cloud" {
		line("console", warnStyle.Render("no password set: console login is not possible"))
	}
	if v.Share != "" {
		line("share", v.Share+dimStyle.Render(" "+glyphTo+" /mnt/host"))
	}
	if len(v.Recipes) > 0 || len(v.Applied) > 0 {
		label := "recipes"
		inConfig := make(map[string]bool, len(v.Recipes))
		for _, r := range v.Recipes {
			inConfig[r] = true
			status := dimStyle.Render("pending")
			if a, ok := v.Applied[r]; ok {
				status = upStyle.Render("applied " + a.At.Format("2006-01-02"))
			}
			line(label, recipeLabel(r)+" ("+status+")")
			label = ""
		}
		// Stale: a recipe that was applied but has since been removed from
		// v.Recipes (the user edited it out of vm.toml after the fact). The
		// applied record survives that edit, so it needs its own status
		// rather than silently vanishing from this pane.
		stale := make([]string, 0, len(v.Applied))
		for name := range v.Applied {
			if !inConfig[name] {
				stale = append(stale, name)
			}
		}
		sort.Strings(stale)
		for _, name := range stale {
			line(label, recipeLabel(name)+" ("+warnStyle.Render("stale - removed from config")+")")
			label = ""
		}
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
	parts = append(parts, renderFooter(detailHelp{
		sshAvailable:    v.State == core.StateRunning,
		consolePassword: consolePasswordAvailable(v),
	}, m.width, m.showHelp))
	// Center for the same reason as the list: the footer is wider than the
	// pane, and a left join would pin the pane to the footer's left edge.
	return lipgloss.JoinVertical(lipgloss.Center, parts...)
}
