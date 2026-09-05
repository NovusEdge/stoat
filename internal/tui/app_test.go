package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func TestTUIViewIncludesOptionalHostFailureAndFix(t *testing.T) {
	checks := []core.HostCheck{
		{Name: "git", Detail: "not found", Optional: true, Fix: []string{"install git"}},
		{Name: "qemu-img", Detail: "not found", Fix: []string{"install qemu-img"}},
	}
	// preflightReport is only the model setup used by Run; the assertion is on
	// the caller-visible rendered View, where optional repair guidance must not
	// disappear.
	m := model{
		screen:    screenList,
		width:     80,
		height:    24,
		list:      newVMList(),
		preflight: preflightReport(checks),
	}
	got := m.View().Content
	if !strings.Contains(got, "git") || !strings.Contains(got, "install git") {
		t.Fatalf("TUI view omitted optional Git repair guidance: %q", got)
	}
	if !strings.Contains(got, "qemu-img") || !strings.Contains(got, "install qemu-img") {
		t.Fatalf("required host failure missing from TUI view: %q", got)
	}
}
