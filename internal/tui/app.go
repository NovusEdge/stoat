package tui

import (
	"errors"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

type screen int

const (
	screenList screen = iota
	screenForm
	screenDetail
	screenEdit
)

type model struct {
	screen screen
	// vms is the last core.List answer: every VM in the data root, good and
	// broken, in one sorted slice. core.List merges them so a caller cannot
	// hold the good ones and forget the broken ones. A broken VM comes back
	// as StateBroken. Dropping broken VMs from this listing has happened
	// before.
	vms    []core.VM
	list   list.Model // owns the VM list's cursor, scrolling and "/" filter
	status string     // a prompt awaiting an answer, shown under the list

	// toast reports a finished action and expires on its own. Anything that
	// needs an answer stays in status instead; see toast.go.
	toast    toast
	toastGen int

	// modal is the image picker, non-nil only while it is open. It owns the
	// keyboard for as long as it is: a modal the user can type past is not a
	// modal.
	modal *imageModal

	preflight string // non-empty when qemu or /dev/kvm is unusable
	width     int
	height    int
	// pendingDelete is the VM awaiting delete confirmation. One field covers
	// broken VMs too: core.Destroy takes a directory name and handles both, so
	// there is no longer a second "which kind of row is this" state to keep
	// mutually exclusive with this one.
	pendingDelete *core.VM

	// provisioning tracks VMs with a provision run in flight. It is keyed by
	// the directory name core.VM.Name reports, like cloudInit below. This
	// stops a second "p" press on the same VM from starting a second ssh
	// session that writes into the same last-provision.log. The value
	// carries what the spinner line shows: when the run started and how far
	// it has got.
	provisioning map[string]provState
	spin         spinner.Model

	// cloudInit holds each cloud VM's last polled cloud-init status. A cloud
	// VM does most of its setup minutes into the boot. Without this,
	// "installing", "finished", and "failed" look the same from outside: a
	// VM rejects a correct password, then silently starts accepting it.
	cloudInit map[string]string
	ciProg    progress.Model // the stage bar beside a cloud VM's setup line

	form      formModel
	edit      editModel
	detail    detailModel
	detailGen int // bumped every time the detail screen is entered; identifies the live tick chain

	showHelp bool // "?" toggles the footer between short and full help
}

// vmsLoadedMsg carries a refreshed VM list. One slice, not two: config.List
// silently omits a directory whose vm.toml won't parse, and pairing it with a
// separate config.ListBroken call left the UI with two sources of truth that
// every new code path had to remember to consult. core.List does that merge
// once and returns the broken ones as StateBroken.
type vmsLoadedMsg struct{ vms []core.VM }

// statusMsg reports that an action finished, errMsg that one failed. They are
// two types rather than one with a flag because every producer already knows
// which it is at the point it returns, and a toast has to colour them
// differently: an ssh failure and "vm deleted" cannot look alike.
type statusMsg string

type errMsg string

func loadVMs() tea.Msg {
	vms, err := core.List()
	if err != nil {
		return errMsg("cannot read " + config.Root() + ": " + err.Error())
	}
	return vmsLoadedMsg{vms: vms}
}

// cfgVM re-materializes the config.VM fields that internal/sshx and
// internal/backend still read: sshx.User, sshx.Args (the ssh identity), and
// backend.For (which backend runs recipes). Those packages do not use core
// yet; moving them is a separate task. This is the one place the TUI crosses
// back to config.VM, instead of the model keeping a second copy of every VM
// beside its core.VM.
//
// It carries only what those three read. It is not a config.VM: Applied,
// Forwards, and everything the edit form writes are absent. Anything that
// needs the real on-disk record loads it by directory name instead; see the
// detail screen.
func cfgVM(v core.VM) *config.VM {
	return &config.VM{
		OS:      v.OS,
		Backend: v.Backend,
		SSHPort: v.SSHPort,
		SSHUser: v.SSHUser,
	}
}

func Run() error {
	if err := config.EnsureRoot(); err != nil {
		return err
	}
	if err := recipes.Install(); err != nil {
		return err
	}
	if err := keys.Ensure(); err != nil {
		return err
	}
	guestErr := guest.Load(filepath.Join(config.Root(), "guests"))
	// ponytail: a log we can't open is not worth refusing to start over.
	// logx.L() falls back to io.Discard, so the TUI just runs without a log.
	_ = logx.Init()
	defer func() { _ = logx.Close() }()
	m := model{
		provisioning: map[string]provState{},
		cloudInit:    map[string]string{},
		ciProg:       newCloudInitProgress(),
		list:         newVMList(),
		spin:         newSpinner(),
	}
	m.preflight = preflightReport(core.Doctor())
	if guestErr != nil {
		// A toast would expire; the preflight block stays until the user acts.
		m.preflight += "\n" + guestErr.Error()
	}
	_, err := tea.NewProgram(m).Run()
	return err
}

// preflightReport renders every failing host check as one block: one line
// per check, plus its fix command when there is one. qemu.Preflight, what
// this replaces, showed only a single string for whichever binary or device
// it hit first. core.Doctor runs every probe and returns a Fix for each
// failure. Showing all of them, each with its fix command, is why this
// switch was made.
func preflightReport(checks []core.HostCheck) string {
	var lines []string
	for _, c := range checks {
		if c.OK {
			continue
		}
		label := c.Name
		if c.Optional {
			label += " (optional)"
		}
		line := label + ": " + c.Detail
		if len(c.Fix) > 0 {
			line += "\n  fix: " + strings.Join(c.Fix, " && ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) Init() tea.Cmd { return loadVMs }

// Update feeds the open byo screen without consuming the message, then runs
// the normal update.
//
// The byo screen needs non-key messages to reach the modal: a filesystem walk
// batch for the scan, a cursor blink for the focused textinput. A diverted
// message must not be swallowed. A tick chain continues only because its
// handler returns the next tick cmd. Consuming one dlTickMsg here would not
// pause the download bar, it would end the chain for good. dlTickMsg's own
// case carries the same warning; routing by screen has broken this once
// already. Copy the message, then carry on.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var browseCmd tea.Cmd
	if m.modal != nil && m.modal.byo {
		if _, isKey := msg.(tea.KeyPressMsg); !isKey {
			cmd, chosen, closed := m.modal.update(msg)
			browseCmd = cmd
			if chosen >= 0 {
				m.form.images = m.modal.images
				m.form.selectImage(chosen)
			}
			if closed {
				m.modal = nil
			}
		}
	}

	next, cmd := m.updateApp(msg)
	if browseCmd == nil {
		return next, cmd
	}
	return next, tea.Batch(cmd, browseCmd)
}

func (m model) updateApp(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ctrl+c must quit from every screen and every sub-mode: delete
	// confirmation, full help, the form, and so on. This handles it
	// centrally, once, instead of duplicating it per screen. That
	// duplication has caused regressions before: a new screen or sub-mode
	// gets added, and its key switch omits the ctrl+c case.
	if key, ok := msg.(tea.KeyPressMsg); ok && key.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// The image picker takes the keyboard while it is open, before any
	// screen sees the message. Only key messages are diverted. The download
	// tick, the cloud-init poll, and the spinner keep running underneath, so
	// opening the picker mid-download does not freeze its progress bar. An
	// open byo screen still needs its non-key results, a scan batch or a
	// cursor blink; Update handles that above by copying instead of
	// consuming.
	if m.modal != nil {
		if _, isKey := msg.(tea.KeyPressMsg); isKey {
			cmd, chosen, closed := m.modal.update(msg)
			if chosen >= 0 {
				// A browsed file is not in m.form.images. It can live anywhere
				// on disk; that is the point. The modal appends it to its own
				// copy and returns an index past the form's original bounds.
				// Re-adopting mo.images is a no-op when nothing was browsed,
				// since it is otherwise the same slice the modal opened with.
				m.form.images = m.modal.images
				m.form.selectImage(chosen)
			}
			if closed {
				m.modal = nil
			}
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// A taller terminal shows more VMs before paginating; the width stays
		// fixed so the pane doesn't breathe with the window.
		m.list.SetWidth(listWidth)
		m.syncListHeight()
		return m, nil
	case vmsLoadedMsg:
		m.vms = msg.vms
		// SetItems returns a Cmd that re-applies an active filter to the new
		// items; dropping it would leave a filtered list showing the old
		// matches after a refresh.
		cmd := m.list.SetItems(vmItems(msg.vms))
		// SetItems does not clamp the cursor. The SetHeight below remaps
		// an out-of-range index to the top, not the bottom. Without this
		// fix, deleting the last VM moves the cursor to the first one,
		// and the next "d" arms a delete on the wrong VM. Guarded on
		// n > 0: SetItems nils the filtered set for a moment, and
		// clamping then would reset a filtered cursor.
		if n := len(m.list.VisibleItems()); n > 0 && m.list.Index() >= n {
			m.list.Select(n - 1)
		}
		m.syncListHeight()
		if anyInstalling(msg.vms) {
			// Kicks the spinner tick chain for a VM installing itself, which
			// never goes through startProvision and so never starts it the
			// way a recipe run does. A redundant kick while the chain is
			// already running is harmless: spinner.Model tags its ticks and
			// drops one that doesn't match its current tag.
			cmd = tea.Batch(cmd, m.spin.Tick)
		}
		return m, cmd
	case statusMsg:
		cmd := m.showToast(string(msg), false)
		return m, cmd
	case errMsg:
		cmd := m.showToast(string(msg), true)
		return m, cmd
	case toastExpiredMsg:
		// Only the timer belonging to the toast currently on screen retires it;
		// an older one whose toast has already been replaced dies here.
		if msg.gen == m.toastGen {
			m.toast = toast{}
		}
		return m, nil
	case spinner.TickMsg:
		// The chain is anchored to work being in flight. When the last
		// provision finishes, it stops re-arming. Each tick also re-reads
		// the tail of every running VM's log. That log tail is where the
		// step and last-output text come from.
		if len(m.provisioning) == 0 && !anyInstalling(m.vms) {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		for name, st := range m.provisioning {
			if v := m.vmByName(name); v != nil {
				st.step, st.last, st.prog, st.hasProg = readProvStep(*v)
				m.provisioning[name] = st
			}
		}
		return m, cmd
	case cloudInitTickMsg:
		if v := m.vmByName(msg.name); v != nil && v.State == core.StateRunning {
			return m, checkCloudInit(*v)
		}
		return m, nil
	case cloudInitMsg:
		m.cloudInit[msg.name] = msg.status
		if cloudInitDone(msg.status) {
			return m, nil
		}
		if v := m.vmByName(msg.name); v != nil && v.State == core.StateRunning {
			return m, pollCloudInit(*v)
		}
		return m, nil
	case vmStartedMsg:
		started := m.showToast(msg.vm.Name+" started", false)
		if msg.vm.Mode == "cloud" {
			// Start watching immediately: the first few polls report
			// "booting…", which is itself the information the user wants.
			m.cloudInit[msg.vm.Name] = "waiting"
			return m, tea.Batch(started, loadVMs, checkCloudInit(msg.vm))
		}
		if msg.vm.Mode == "disk" && !msg.vm.Installed {
			// The installer is running now, not the system `apply` needs to
			// reach. Watch for it to power off and restart into the disk;
			// the spinner tick keeps the "installing" line animating while
			// that happens.
			return m, tea.Batch(started, loadVMs, awaitInstall(msg.vm), m.spin.Tick)
		}
		if !needsAutoProvision(msg.vm) {
			return m, tea.Batch(started, loadVMs)
		}
		// Watch for sshd in the background. The user keeps full use of the UI
		// meanwhile: provisioning starts on its own once the VM is reachable.
		return m, tea.Batch(started, loadVMs, awaitSSH(msg.vm))
	case installRestartedMsg:
		v := m.vmByName(msg.name)
		// Re-check on arrival: the restart itself just happened, but the
		// user could have stopped or deleted the VM in the meantime.
		if v == nil || v.State != core.StateRunning || !v.Installed {
			return m, nil
		}
		if m.pendingDelete != nil {
			return m, nil
		}
		return m, awaitSSH(*v)
	case sshReadyMsg:
		v := m.vmByName(msg.name)
		// Re-check on arrival: up to 90 seconds have passed, in which the VM
		// could have been stopped, deleted, edited to drop its recipes, or
		// provisioned by hand.
		if v == nil || v.State != core.StateRunning || !needsAutoProvision(*v) {
			return m, nil
		}
		if _, busy := m.provisioning[v.Name]; busy {
			return m, nil
		}
		// A pending delete is a more consequential question and the user is
		// mid-answer; starting a provision now would clear m.status and hide
		// the confirmation.
		if m.pendingDelete != nil {
			return m, nil
		}
		return m, m.startProvision(*v)
	case provisionDoneMsg:
		delete(m.provisioning, msg.name)
		if errors.Is(msg.err, core.ErrProvisionInProgress) {
			// m.provisioning already stops a second provision started from this
			// process. This is the cross-process case: another stoat process (a
			// second TUI, or the CLI) holds the VM's lock. Not an error toast,
			// since the user did nothing wrong.
			cmd := m.showToast(msg.name+": recipes already applying", false)
			return m, cmd
		}
		if msg.err != nil {
			cmd := m.showToast(msg.name+": "+msg.err.Error(), true)
			return m, cmd
		}
		cmd := m.showToast(msg.name+": recipes applied", false)
		return m, cmd
	case vmSavedMsg:
		// Adopt the saved VM only now. saveEdit returns a statusMsg on
		// failure instead, so the panes never show state that was not
		// persisted. detail.vm is a core.VM, not the *config.VM msg.vm
		// carries. It is re-derived by directory rather than assigned
		// directly.
		cv, err := core.Get(filepath.Base(msg.vm.Dir))
		if err != nil {
			cmd := m.showToast(err.Error(), true)
			return m, cmd
		}
		m.detail.vm = cv
		m.edit.vm = msg.vm
		m.screen = screenDetail
		// Re-arm the detail ticker for the same reason as the edit form's
		// esc path: it dies while any other screen is showing.
		m.detailGen++
		saved := m.showToast(msg.vm.Name+" saved"+msg.note, false)
		return m, tea.Batch(saved, loadVMs, tick(m.detailGen))
	case screenMsg:
		m.screen = screen(msg)
		m.showHelp = false
		return m, nil
	case dlTickMsg, imageFetchedMsg, imageFetchErrMsg:
		// A download outlives the form. "esc" returns to the list while the
		// fetch goroutine keeps running; there is no way to cancel it.
		// Routing its messages by screen would strand them: the tick chain
		// would die, and a checksum failure would go unreported. They go
		// to the form's handler regardless of the current screen, like
		// provisionDoneMsg above.
		return m.updateForm(msg)
	}

	switch m.screen {
	case screenForm:
		return m.updateForm(msg)
	case screenEdit:
		return m.updateEdit(msg)
	case screenDetail:
		return m.updateDetail(msg)
	default:
		return m.updateList(msg)
	}
}

// smallWidth and smallHeight are the floor below which panes are refused
// rather than rendered corrupted: narrower or shorter than this, a bordered
// box has nowhere to wrap that doesn't come out as garbage.
const (
	smallWidth  = 60
	smallHeight = 20
)

func (m model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		// No WindowSizeMsg has arrived yet (bubbletea renders once before
		// the first one); nothing sensible to size a pane against.
		return m.newView("")
	}
	if m.width < smallWidth || m.height < smallHeight {
		return m.newView(lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			warnStyle.Render("terminal too small: resize to at least 60x20")))
	}

	var body string
	switch m.screen {
	case screenForm:
		body = m.viewForm()
	case screenEdit:
		body = m.viewEdit()
	case screenDetail:
		body = m.viewDetail()
	default:
		body = m.viewList()
	}

	// JoinVertical(Center, ...) centers each block as a whole instead of
	// padding every line to a shared width first. A hand-rolled version of
	// this used to produce a justified-text look; this is the fix. The list
	// screen's banner sits above the body as a heading. Every other screen's
	// body already carries its own pane title, so it needs no separate
	// banner.
	s := body
	if m.screen == screenList {
		s = lipgloss.JoinVertical(lipgloss.Center, banner(), "", body)
	}

	// Centered on both axes. A centered screen re-centers vertically when its
	// height changes (a toast, a progress panel, a search prompt), so content
	// shifts as those appear. The user prefers a centered frame to a
	// top-anchored one, so the shift is the accepted cost.
	//
	// Content taller than the terminal falls back to a top anchor, or Place
	// would clip the top and leave it unreachable.
	vAlign := lipgloss.Center
	if lipgloss.Height(s) > m.height {
		vAlign = lipgloss.Top
	}
	// Overlays go on last, over the finished screen: they must not be part of
	// what Place centers. The modal sits under the toast, so a toast raised
	// while the picker is open is still readable.
	return m.newView(m.renderToast(m.renderModal(lipgloss.Place(m.width, m.height, lipgloss.Center, vAlign, s))))
}

// newView wraps a rendered frame in a tea.View with the alt-screen flag set,
// the v1 equivalent of passing tea.WithAltScreen() to tea.NewProgram, which
// v2 moved from a program-startup option to a per-frame View field.
func (m model) newView(s string) tea.View {
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}
