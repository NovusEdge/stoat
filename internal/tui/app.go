package tui

import (
	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
)

type screen int

const (
	screenList screen = iota
	screenForm
	screenDetail
)

type model struct {
	screen              screen
	vms                 []*config.VM
	broken              []config.Broken // VMs whose vm.toml exists but fails to parse
	cursor              int
	status              string // transient message shown under the list
	preflight           string // non-empty when qemu or /dev/kvm is unusable
	width               int
	height              int
	pendingDelete       *config.VM // VM awaiting delete confirmation
	pendingDeleteBroken string     // name of a broken VM dir awaiting delete confirmation; mutually exclusive with pendingDelete

	// provisioning tracks VMs with a provision run in flight, keyed by
	// name, so a second "p" press on the same VM can't start a second ssh
	// session writing into the same last-provision.log.
	provisioning map[string]bool

	form      formModel
	detail    detailModel
	detailGen int // bumped every time the detail screen is entered; identifies the live tick chain
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
	m := model{provisioning: map[string]bool{}}
	if err := qemu.Preflight(); err != nil {
		m.preflight = err.Error()
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd { return loadVMs }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case vmsLoadedMsg:
		m.vms = msg.vms
		m.broken = msg.broken
		total := len(m.vms) + len(m.broken)
		if m.cursor >= total {
			m.cursor = max(0, total-1)
		}
		return m, nil
	case statusMsg:
		m.status = string(msg)
		return m, nil
	case provisionDoneMsg:
		delete(m.provisioning, msg.name)
		if msg.err != nil {
			m.status = msg.name + ": " + msg.err.Error()
		} else {
			m.status = msg.name + " provisioned"
		}
		return m, nil
	case screenMsg:
		m.screen = screen(msg)
		return m, nil
	}

	switch m.screen {
	case screenForm:
		return m.updateForm(msg)
	case screenDetail:
		return m.updateDetail(msg)
	default:
		return m.updateList(msg)
	}
}

func (m model) View() string {
	var s string
	switch m.screen {
	case screenForm:
		s = m.viewForm()
	case screenDetail:
		s = m.viewDetail()
	default:
		s = m.viewList()
	}

	if m.width == 0 || m.height == 0 {
		// No WindowSizeMsg has arrived yet (bubbletea renders once before
		// the first one); placing into a 0x0 box would blank the screen.
		return s
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
