package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/qemu"
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
	screen              screen
	vms                 []*config.VM
	broken              []config.Broken // VMs whose vm.toml exists but fails to parse
	list                list.Model      // owns the VM list's cursor, scrolling and "/" filter
	status              string          // transient message shown under the list
	preflight           string          // non-empty when qemu or /dev/kvm is unusable
	width               int
	height              int
	pendingDelete       *config.VM // VM awaiting delete confirmation
	pendingProvision    *config.VM // VM that just became reachable, awaiting a y/N to provision
	pendingDeleteBroken string     // name of a broken VM dir awaiting delete confirmation; mutually exclusive with pendingDelete

	// provisioning tracks VMs with a provision run in flight, keyed by name,
	// so a second "p" press on the same VM can't start a second ssh session
	// writing into the same last-provision.log. The value carries what the
	// spinner line shows: when it started and where it has got to.
	provisioning map[string]provState
	spin         spinner.Model

	// cloudInit holds each cloud VM's last polled cloud-init status. A cloud
	// VM does most of its setup minutes into the boot, so without this
	// "installing", "finished" and "failed" are indistinguishable from the
	// outside — a VM rejects a correct password and then silently starts
	// accepting it.
	cloudInit map[string]string
	ciProg    progress.Model // the stage bar beside a cloud VM's setup line

	form      formModel
	edit      editModel
	detail    detailModel
	detailGen int // bumped every time the detail screen is entered; identifies the live tick chain

	showHelp bool // "?" toggles the footer between short and full help
}

// vmsLoadedMsg carries a refreshed VM list, alongside any VM directories
// whose vm.toml exists but failed to parse — config.List silently omits
// those, so they're fetched separately and surfaced rather than made to
// look deleted.
type vmsLoadedMsg struct {
	vms    []*config.VM
	broken []config.Broken
}

// statusMsg reports the outcome of an action.
type statusMsg string

func loadVMs() tea.Msg {
	vms, err := config.List()
	if err != nil {
		return statusMsg("cannot read " + config.Root() + ": " + err.Error())
	}
	// A failure here just means broken VMs silently don't show up this
	// refresh; it must never block loading the good ones.
	broken, _ := config.ListBroken()
	return vmsLoadedMsg{vms: vms, broken: broken}
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
	// ponytail: a log we can't open is not worth refusing to start over —
	// logx.L() falls back to io.Discard, so the TUI just runs without a log.
	_ = logx.Init()
	defer logx.Close()
	m := model{
		provisioning: map[string]provState{},
		cloudInit:    map[string]string{},
		ciProg:       newCloudInitProgress(),
		list:         newVMList(),
		spin:         newSpinner(),
	}
	if err := qemu.Preflight(); err != nil {
		m.preflight = err.Error()
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd { return loadVMs }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ctrl+c must quit from every screen and every sub-mode (delete
	// confirmation, full help, the form, ...). It's handled centrally, once,
	// here — rather than duplicated per-screen — because that duplication is
	// exactly how it has regressed before: a new screen or sub-mode gets
	// added, and whoever writes its key switch doesn't think to repeat the
	// ctrl+c case.
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		return m, tea.Quit
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
		m.broken = msg.broken
		// SetItems returns a Cmd that re-applies an active filter to the new
		// items; dropping it would leave a filtered list showing the old
		// matches after a refresh.
		cmd := m.list.SetItems(vmItems(msg.vms, msg.broken))
		// SetItems does not clamp the cursor, and the SetHeight below remaps
		// an out-of-range index to the TOP rather than the bottom. Without
		// this, deleting the last VM in the list moves the cursor to the
		// first one — and the next "d" arms a delete on the wrong VM.
		// Guarded on n > 0 because SetItems nils the filtered set for a
		// moment, and clamping then would reset a filtered cursor.
		if n := len(m.list.VisibleItems()); n > 0 && m.list.Index() >= n {
			m.list.Select(n - 1)
		}
		m.syncListHeight()
		return m, cmd
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case spinner.TickMsg:
		// The chain is anchored to there being work in flight: when the last
		// provision finishes it simply stops re-arming. Each tick also
		// re-reads the tail of every running VM's log, which is where the
		// step and last-output text come from.
		if len(m.provisioning) == 0 {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		for name, st := range m.provisioning {
			if v := m.vmByName(name); v != nil {
				st.step, st.last = readProvStep(v)
				m.provisioning[name] = st
			}
		}
		return m, cmd
	case cloudInitTickMsg:
		if v := m.vmByName(msg.name); v != nil && qemu.Running(v) {
			return m, checkCloudInit(v)
		}
		return m, nil
	case cloudInitMsg:
		m.cloudInit[msg.name] = msg.status
		if cloudInitDone(msg.status) {
			return m, nil
		}
		if v := m.vmByName(msg.name); v != nil && qemu.Running(v) {
			return m, pollCloudInit(v)
		}
		return m, nil
	case vmStartedMsg:
		m.status = msg.vm.Name + " started"
		if msg.vm.Mode == "cloud" {
			// Start watching immediately: the first few polls report
			// "booting…", which is itself the information the user wants.
			m.cloudInit[msg.vm.Name] = "waiting"
			return m, tea.Batch(loadVMs, checkCloudInit(msg.vm))
		}
		if !wantsAutoProvisionPrompt(msg.vm) {
			return m, loadVMs
		}
		// Watch for sshd in the background. The user keeps full use of the UI
		// meanwhile — this is an offer that arrives when it is ready, not a
		// modal wait.
		return m, tea.Batch(loadVMs, awaitSSH(msg.vm))
	case sshReadyMsg:
		v := m.vmByName(msg.name)
		// Re-check on arrival: up to 90 seconds have passed, in which the VM
		// could have been stopped, deleted, edited to drop its recipes, or
		// provisioned by hand.
		if v == nil || !qemu.Running(v) || !wantsAutoProvisionPrompt(v) {
			return m, nil
		}
		if _, busy := m.provisioning[v.Name]; busy {
			return m, nil
		}
		// Never stack prompts: a pending delete is a more consequential
		// question and the user is mid-answer.
		if m.pendingDelete != nil || m.pendingDeleteBroken != "" {
			return m, nil
		}
		m.pendingProvision = v
		m.status = autoProvisionPrompt(v)
		return m, nil
	case provisionDoneMsg:
		delete(m.provisioning, msg.name)
		if msg.err != nil {
			m.status = msg.name + ": " + msg.err.Error()
		} else {
			m.status = msg.name + " provisioned"
		}
		return m, nil
	case vmSavedMsg:
		// Adopt the saved VM only now: saveEdit returns a statusMsg instead
		// on failure, so the panes never show state that wasn't persisted.
		m.detail.vm = msg.vm
		m.edit.vm = msg.vm
		m.screen = screenDetail
		m.status = msg.vm.Name + " saved" + msg.note
		// Re-arm the detail ticker for the same reason as the edit form's
		// esc path: it dies while any other screen is showing.
		m.detailGen++
		return m, tea.Batch(loadVMs, tick(m.detailGen))
	case screenMsg:
		m.screen = screen(msg)
		m.showHelp = false
		return m, nil
	case dlTickMsg, imageFetchedMsg, imageFetchErrMsg:
		// A download outlives the form: "esc" returns to the list while the
		// fetch goroutine keeps running, and there is no way to cancel it.
		// Routing its messages by screen would strand them — the tick chain
		// would die, and a checksum failure would be swallowed with the user
		// never told. They go to the form's handler wherever we are, exactly
		// as provisionDoneMsg is handled centrally above.
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
// rather than rendered corrupted — narrower or shorter than this, a bordered
// box has nowhere to wrap that doesn't come out as garbage.
const (
	smallWidth  = 60
	smallHeight = 20
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		// No WindowSizeMsg has arrived yet (bubbletea renders once before
		// the first one); nothing sensible to size a pane against.
		return ""
	}
	if m.width < smallWidth || m.height < smallHeight {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			warnStyle.Render("terminal too small — resize to at least 60x20"))
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

	// JoinVertical(Center, ...) centers each block as a whole rather than
	// padding every line to a shared width first — the fix for the
	// justified-text look a hand-rolled version of this used to produce.
	// The list screen's banner sits above the body as a heading; every
	// other screen's body already carries its own pane title, so it has no
	// separate banner to join.
	s := body
	if m.screen == screenList {
		s = lipgloss.JoinVertical(lipgloss.Center, banner(), "", body)
	}

	vAlign := lipgloss.Center
	if lipgloss.Height(s) > m.height {
		// Content taller than the terminal: centering vertically would clip
		// it top and bottom with no way to scroll back to what's lost.
		// Anchor to the top instead so everything stays reachable.
		vAlign = lipgloss.Top
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, vAlign, s)
}
