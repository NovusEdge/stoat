package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
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
	screen        screen
	vms           []*config.VM
	cursor        int
	status        string // transient message shown under the list
	preflight     string // non-empty when qemu or /dev/kvm is unusable
	width         int
	height        int
	pendingDelete *config.VM // VM awaiting delete confirmation

	// provisioning tracks VMs with a provision run in flight, keyed by
	// name, so a second "p" press on the same VM can't start a second ssh
	// session writing into the same last-provision.log.
	provisioning map[string]bool

	form      formModel
	detail    detailModel
	detailGen int // bumped every time the detail screen is entered; identifies the live tick chain
}

// vmsLoadedMsg carries a refreshed VM list.
type vmsLoadedMsg []*config.VM

// statusMsg reports the outcome of an action.
type statusMsg string

func loadVMs() tea.Msg {
	vms, err := config.List()
	if err != nil {
		return statusMsg("cannot read " + config.Root() + ": " + err.Error())
	}
	return vmsLoadedMsg(vms)
}

func Run() error {
	if err := config.EnsureRoot(); err != nil {
		return err
	}
	if err := recipes.Install(); err != nil {
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
		m.vms = msg
		if m.cursor >= len(m.vms) {
			m.cursor = max(0, len(m.vms)-1)
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
	switch m.screen {
	case screenForm:
		return m.viewForm()
	case screenDetail:
		return m.viewDetail()
	default:
		return m.viewList()
	}
}
