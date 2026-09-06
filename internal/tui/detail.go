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

// name reports the VM's directory, not the vm.toml `name` field. The "E" key
// can edit that field to something else entirely. See core.VM.Name's comment
// for the bug this rule prevents. core.VM.Name already returns the directory;
// this exists so every call site below reads "the VM's identity" instead of a
// raw field access.
func (d detailModel) name() string { return d.vm.Name }

// coreVM asks core for the VM's current state, fresh at call time. d.vm is a
// snapshot taken when this screen was last entered or reloaded. Another
// terminal can start or stop the VM before the user acts on it. The "s" and
// "p" keys must act on current state, not a stale snapshot, so they call this
// instead of reading d.vm directly.
func (d detailModel) coreVM() (core.VM, error) { return core.Get(d.name()) }

// tickMsg carries the generation of the detail-screen visit that scheduled
// it. updateDetail re-arms the chain only when gen still matches
// m.detailGen. Every other visit to the detail screen bumps detailGen, so a
// stale chain left over from a prior visit dies silently on its next tick
// instead of re-arming and running forever alongside the live one.
type tickMsg struct {
	t   time.Time
	gen int
}

func tick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg{t: t, gen: gen} })
}

// tailLog reads the last n lines of the most recent recipe-apply run. It goes
// through core.Logs rather than opening ProvisionLogPath directly. Logs
// resolves name to the right file. It returns an empty reader, not an error,
// for a VM that has never had recipes applied or has a broken vm.toml.
func tailLog(name string, n int) string {
	r, err := core.Logs(name, core.WhichApply)
	if err != nil {
		return ""
	}
	defer func() { _ = r.Close() }()
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

	// The snapshots modal owns the keyboard while it's open, like the pager
	// above. Every message goes to it first. It reports whether it should
	// close rather than reacting to a bare esc here: esc means different
	// things inside it, cancel a sub-prompt or close outright. See
	// snapshotsModal.update.
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
			// The edit screen writes vm.toml directly through core.Update. It
			// needs the real config.VM, which this screen no longer holds.
			// Loaded by directory, per the identity rule above.
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
				// By directory. This used to reload by m.detail.vm.Name, the
				// vm.toml field. That field is the one thing the editor
				// session can change: renaming a VM in the file made the
				// reload miss it, or load a different VM.
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
			// core.Update takes the data-root lock and writes vm.toml. This
			// replaced a second, unlocked Save() call that wrote straight to
			// the file, bypassing the lock every other mutation uses. The
			// in-memory toggle updates only after the write to disk lands. A
			// failed Update then cannot leave the pane showing a state that
			// was never persisted.
			next := !v.Installed
			updated, err := core.Update(v.Name, core.Patch{Installed: &next})
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			m.detail.vm = updated
			cmd := m.showToast(fmt.Sprintf("%s installed=%v", updated.Name, updated.Installed), false)
			return m, tea.Batch(loadVMs, cmd)
		case "d":
			v := m.detail.vm
			// "auto" writes back as "", config.VM.Display's own default for a
			// vm.toml that never set a preference.
			next := nextDisplayPref(v.Display)
			stored := next
			if stored == "auto" {
				stored = ""
			}
			updated, err := core.Update(v.Name, core.Patch{Display: &stored})
			if err != nil {
				cmd := m.showToast(err.Error(), true)
				return m, cmd
			}
			m.detail.vm = updated
			note := "display: " + displayPrefLabel(updated.Display) + " (applies at next start)"
			if v.State == core.StateRunning {
				// Matches edit.go's saveEdit note for a running VM: this pane
				// builds no restart flow of its own.
				note += "; restart to apply"
			}
			cmd := m.showToast(note, false)
			return m, cmd
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

// consolePasswordAvailable reports whether v has a console password to type
// or copy right now. The VM must be running: the monitor socket and the
// login prompt exist only then. It must also have a password set. The
// "console" row in viewDetail and qemu.consoleCredential's log line use the
// same rule. v.State already answers "is qemu running" as of core.Get's
// call, so this makes no separate qemu.Running call.
func consolePasswordAvailable(v core.VM) bool {
	return v.State == core.StateRunning && v.ConsolePassword != ""
}

// qemuMonitorVM builds the minimal *config.VM that qemu.TypeConsolePassword
// needs to dial v's monitor socket. MonitorPath is a filepath.Join off Dir,
// so Dir plus the password is everything the call requires. This avoids
// loading a second copy of the on-disk record. app.go's cfgVM uses the same
// shim to cross the same boundary for sshx.
func qemuMonitorVM(v core.VM) *config.VM {
	return &config.VM{Name: v.Name, Dir: v.Paths.Dir, ConsolePassword: v.ConsolePassword}
}

// typeConsolePassword has qemu type v's console password into the guest, off
// the UI goroutine. Sending it can take a while: one monitor round trip per
// character, paced deliberately (see qemu.TypeConsolePassword). The TUI must
// stay responsive while that runs.
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
		parts := []string{pane("", dimStyle.Render("no vm selected"), m.width), "", warnStyle.Render(m.status)}
		parts = append(parts, dimStyle.Render("esc back"))
		return column(appContentWidth, parts...)
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

	facts := fields{width: appContentWidth}
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
		// Until this is true, the VM boots its installer ISO. An apkovl-backend
		// (Alpine) disk VM runs setup-alpine unattended and installs itself;
		// any other backend needs its installer run at the console. The next
		// start sets this once the install writes to the disk; "i" overrides a
		// wrong guess.
		hint := "installing on first boot"
		if v.Backend != "apkovl" {
			hint = "run " + installerName(v.OS) + " in the console, then restart"
		}
		installed := warnStyle.Render("no: " + hint)
		if v.Installed {
			installed = upStyle.Render("yes")
		}
		line("installed", installed)
	}
	sshUser := sshx.User(cfgVM(v))
	line("ssh", fmt.Sprintf("%s@127.0.0.1:%d", sshUser, v.SSHPort))
	// Forwards are otherwise invisible everywhere in the TUI (see the
	// migration plan's D1). Showing them here lets a user notice a port
	// collision before the edit screen creates one. Omitted entirely when
	// there are none, matching every other optional row above.
	if len(v.Forwards) > 0 {
		label := "forward"
		for _, f := range v.Forwards {
			line(label, fmt.Sprintf("%d %s %d (guest)", f.HostPort, glyphTo, f.GuestPort))
			label = ""
		}
		// qemu's user-mode netdev argv is fixed at process launch. It cannot
		// hot-add a hostfwd rule to a running process. A forward declared
		// while the VM is running saves to vm.toml and applies later, but
		// has no effect on the qemu process already up.
		// This distinction stays visible: "applied later" must never read
		// like "already true".
		// This renders as its own line, in its own color, so it cannot be
		// mistaken for a caption on the rows above it.
		effect := upStyle.Render("in effect")
		if v.State == core.StateRunning {
			effect = warnStyle.Render("saved, applies at next start (not the running process)")
		}
		facts.row("", "", effect)
	}
	line("display", displayPrefLabel(v.Display))
	// qemu.DisplayKind takes pref and host graphical directly, not a
	// *config.VM, so a core.VM caller can call it without going through
	// qemu.NeedsWindow/WantsWindow, which need a *config.VM.
	//
	// A bare socket path is not enough: the user needs the actual command
	// that opens it, so this prints one for a viewer installed on this host.
	//
	// With no graphical session, a VM that would otherwise get a window also
	// lands on this socket. That is the case where the user needs the
	// explanation most, since the VM would otherwise look like it refused
	// to start.
	if graphical := qemu.GraphicalSession(); qemu.DisplayKind(v.Display, graphical) != qemu.DisplayWindow {
		if !graphical && qemu.DisplayKind(v.Display, true) == qemu.DisplayWindow {
			facts.row("", "", warnStyle.Render("no usable graphical session on this host: falling back to vnc"))
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
	// The console line matters most for cloud images. Cloud images lock
	// every account, so without a password the console shows a login prompt
	// with no valid answer. The moment a user needs it is the moment ssh has
	// failed. This password is set only by the cloudinit backend (form.go),
	// which is always cloud mode; the surface it is typed at is whichever one
	// is shown above, a qemu window or the VNC socket.
	if v.ConsolePassword != "" {
		surface := "the qemu window"
		if qemu.DisplayKind(v.Display, qemu.GraphicalSession()) == qemu.DisplayVNC {
			surface = "vnc, above"
		}
		line("console", sshUser+" / "+v.ConsolePassword+dimStyle.Render("  (over "+surface+")"))
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
			if state, ok := recipeStateByName(v.RecipeStates, r); ok {
				if state.Applied {
					status = upStyle.Render("applied " + state.Health)
				}
			} else if a, ok := v.Applied[r]; ok {
				status = upStyle.Render("applied " + a.At.Format("2006-01-02"))
			}
			line(label, recipeLabel(r)+" ("+status+")")
			label = ""
			if state, ok := recipeStateByName(v.RecipeStates, r); ok {
				for _, name := range sortedKeys(state.Params) {
					value := state.Params[name]
					if isSecretParam(state, name) && value != core.SecretUnset {
						value = core.SecretSet
					}
					line("", "param "+name+": "+value)
				}
				for _, name := range sortedKeys(state.Outputs) {
					line("", "out "+name+": "+state.Outputs[name])
				}
			}
		}
		// Stale means applied but since removed from v.Recipes: the user
		// edited it out of vm.toml. The applied record survives that edit.
		// It needs its own status instead of silently vanishing from this
		// pane.
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

	factsBox := paneAt(v.Name, facts.String(), appContentWidth, m.width)

	parts := []string{factsBox}
	if m.detail.log != "" {
		// lipgloss re-applies the style on every line of a multi-line string,
		// so the log needs no per-line loop of its own.
		parts = append(parts, "", paneAt("last apply", dimStyle.Render(m.detail.log), appContentWidth, m.width))
	}

	if panel := progressPanel(m); panel != "" {
		parts = append(parts, "", panel)
	}

	parts = append(parts, "")
	parts = append(parts, provLinesExcept(m, v.Name)...)
	// An always-present slot: an appearing status line replaces blank
	// space instead of pushing the footer down.
	parts = append(parts, warnStyle.Render(m.status))
	parts = append(parts, renderFooter(detailHelp{
		sshAvailable:    v.State == core.StateRunning,
		consolePassword: consolePasswordAvailable(v),
	}, m.width, m.showHelp))
	return column(appContentWidth, parts...)
}

func recipeStateByName(states []core.RecipeState, name string) (core.RecipeState, bool) {
	for _, state := range states {
		if state.Name == name {
			return state, true
		}
	}
	return core.RecipeState{}, false
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isSecretParam(state core.RecipeState, name string) bool {
	for _, secret := range state.SecretNames {
		if secret == name {
			return true
		}
	}
	return false
}
