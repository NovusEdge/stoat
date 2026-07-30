package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
)

// detailModel is a placeholder for the VM detail screen.
// replaced in Task 8
type detailModel struct{}

// newDetail is a placeholder constructor for detailModel.
// replaced in Task 8
func newDetail(v *config.VM) detailModel { return detailModel{} }

// updateDetail is a placeholder update handler for screenDetail. It only
// handles enough keys to not trap the user: ctrl+c quits, esc/q return to
// the list.
// replaced in Task 8
func (m model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc", "q":
			m.screen = screenList
		}
	}
	return m, nil
}

// viewDetail is a placeholder view for screenDetail.
// replaced in Task 8
func (m model) viewDetail() string { return "" }
