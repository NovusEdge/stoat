package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

// TestInstallerName: a known OS with an Installer in the registry gets named
// exactly; an unknown OS or one with no Installer stays generic. Naming the
// wrong installer is worse than staying general.
func TestInstallerName(t *testing.T) {
	cases := []struct {
		os, want string
	}{
		{"alpine", "setup-alpine"},
		{"ubuntu", "the installer"},
		{"nonexistent-os", "the installer"},
		{"", "the installer"},
	}
	for _, c := range cases {
		got := installerName(c.os)
		if got != c.want {
			t.Errorf("installerName(OS=%q) = %q, want %q", c.os, got, c.want)
		}
	}
}

// TestStartProvisionRefusesZeroRecipes: core.Apply on zero recipes returns
// nil (a legitimate no-op), which used to surface here as a false
// "provisioned" success. startProvision must still short-circuit before
// core.Apply is ever reached.
func TestStartProvisionRefusesZeroRecipes(t *testing.T) {
	m := model{provisioning: map[string]provState{}, spin: newSpinner()}
	v := core.VM{
		Name: "no-recipes", Mode: "live", OS: "alpine", Installed: true,
		SSHPort: 2204, Recipes: nil,
		Paths: core.Paths{Dir: t.TempDir()},
	}

	m.startProvision(v)

	if len(m.provisioning) != 0 {
		t.Error("a VM with zero recipes was marked as provisioning anyway")
	}
	if !strings.Contains(m.toast.text, "no recipes selected") {
		t.Errorf("toast = %q, expected the zero-recipes refusal", m.toast.text)
	}
}
