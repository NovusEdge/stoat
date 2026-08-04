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

// TestStartProvisionRefusesByBackendNotMode is the regression test for D9a:
// the edit screen's mode switch can leave a VM with mode="disk" and
// backend="cloudinit", a state core.Apply refuses with ErrAppliedAtBoot.
// Keying the refusal on v.Mode == "cloud" would miss it entirely and let a
// cloud-init fragment be piped into `sh -s` over ssh as a shell script.
func TestStartProvisionRefusesByBackendNotMode(t *testing.T) {
	m := model{provisioning: map[string]provState{}, spin: newSpinner()}
	v := core.VM{
		Name: "mode-switched", Mode: "disk", Backend: "cloudinit", Installed: true,
		SSHPort: 2203, Recipes: []string{"xfce.alpine.cloud.yaml"},
		Paths: core.Paths{Dir: t.TempDir()},
	}

	m.startProvision(v)

	if len(m.provisioning) != 0 {
		t.Error("a cloudinit-backed VM was marked as provisioning anyway")
	}
	if !strings.Contains(m.toast.text, "cloud-init") {
		t.Errorf("toast = %q, expected the cloud-init refusal", m.toast.text)
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
