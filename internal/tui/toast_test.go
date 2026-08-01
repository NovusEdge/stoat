package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/novusedge/stoat/internal/config"
)

// The overlay must not change the shape of what it is drawn over: the screen
// is already sized to the terminal, so a toast that added a line or a column
// would push the layout off the bottom or wrap it.
func TestToastOverlayKeepsScreenShape(t *testing.T) {
	m := model{provisioning: map[string]provState{}, cloudInit: map[string]string{},
		ciProg: newCloudInitProgress(), list: newVMList(), spin: newSpinner(),
		width: 100, height: 30}
	m.list.SetItems(vmItems([]*config.VM{{Name: "vm1", Mode: "live", RAM: 1024, CPUs: 2, SSHPort: 2200}}, nil))

	plain := m.View().Content
	m.showToast("vm1 stopped", false)
	withToast := m.View().Content

	if got, want := lipgloss.Height(withToast), lipgloss.Height(plain); got != want {
		t.Errorf("height %d, want %d", got, want)
	}
	if got, want := lipgloss.Width(withToast), lipgloss.Width(plain); got != want {
		t.Errorf("width %d, want %d", got, want)
	}
	if !strings.Contains(withToast, "vm1 stopped") {
		t.Error("toast text missing from the rendered screen")
	}
}

// A toast replaced by a newer one must not be retired by the older one's
// timer, or the second toast vanishes early.
func TestStaleToastTimerDoesNotClearTheCurrentToast(t *testing.T) {
	var m model
	m.showToast("first", false)
	stale := m.toastGen
	m.showToast("second", true)

	mm, _ := m.Update(toastExpiredMsg{gen: stale})
	if got := mm.(model).toast.text; got != "second" {
		t.Errorf("toast = %q, want %q — the stale timer cleared the live toast", got, "second")
	}
	mm, _ = mm.(model).Update(toastExpiredMsg{gen: mm.(model).toastGen})
	if got := mm.(model).toast.text; got != "" {
		t.Errorf("toast = %q, want it retired by its own timer", got)
	}
}
