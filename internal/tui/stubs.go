package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
)

// formModel is a placeholder for the create-VM form.
// replaced in Task 7
type formModel struct{}

// newForm is a placeholder constructor for formModel.
// replaced in Task 7
func newForm() formModel { return formModel{} }

// updateForm is a placeholder update handler for screenForm.
// replaced in Task 7
func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

// viewForm is a placeholder view for screenForm.
// replaced in Task 7
func (m model) viewForm() string { return "" }

// detailModel is a placeholder for the VM detail screen.
// replaced in Task 8
type detailModel struct{}

// newDetail is a placeholder constructor for detailModel.
// replaced in Task 8
func newDetail(v *config.VM) detailModel { return detailModel{} }

// updateDetail is a placeholder update handler for screenDetail.
// replaced in Task 8
func (m model) updateDetail(msg tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

// viewDetail is a placeholder view for screenDetail.
// replaced in Task 8
func (m model) viewDetail() string { return "" }
