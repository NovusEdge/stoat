package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
)

func autoVM(t *testing.T, mode string, recipes []string, log string) *config.VM {
	t.Helper()
	dir := t.TempDir()
	if log != "" {
		if err := os.WriteFile(filepath.Join(dir, "last-provision.log"), []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &config.VM{
		Name: "vm", Mode: mode, OS: "alpine", Installed: true,
		SSHPort: 2200, Dir: dir, Recipes: recipes,
	}
}

// TestWantsAutoProvisionPrompt covers who gets offered and who doesn't. The
// live-vs-disk difference is not a preference: a live VM's root is a tmpfs
// overlay, so a previous run is genuinely gone after the reboot, while a disk
// VM's packages are still there.
func TestWantsAutoProvisionPrompt(t *testing.T) {
	const ok = "=== recipe xfce.alpine.sh ===\nOK: 378 packages\n\ndone\n"
	const failed = "waiting for ssh on port 2200…\nFAILED: vm: ssh not reachable\n"
	recipes := []string{"xfce.alpine.sh"}

	cases := []struct {
		name string
		vm   *config.VM
		want bool
	}{
		{"live, never provisioned", autoVM(t, "live", recipes, ""), true},
		{"live, previously succeeded — the reboot wiped it", autoVM(t, "live", recipes, ok), true},
		{"disk, never provisioned", autoVM(t, "disk", recipes, ""), true},
		{"disk, previously succeeded — still installed", autoVM(t, "disk", recipes, ok), false},
		{"disk, previous run failed", autoVM(t, "disk", recipes, failed), true},
		{"no recipes", autoVM(t, "live", nil, ""), false},
		{"cloud — cloud-init already did it", autoVM(t, "cloud", recipes, ""), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wantsAutoProvisionPrompt(c.vm); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}

	// A disk VM with no OS installed never becomes reachable at all.
	uninstalled := autoVM(t, "disk", recipes, "")
	uninstalled.Installed = false
	if wantsAutoProvisionPrompt(uninstalled) {
		t.Error("offered to provision a disk VM with no OS installed")
	}
}

func TestLastProvisionSucceeded(t *testing.T) {
	cases := []struct {
		name, log string
		want      bool
	}{
		{"clean run", "=== recipe x ===\nOK\n\ndone\n", true},
		{"trailing blank lines", "done\n\n\n", true},
		{"failed", "FAILED: recipe x: exit 1\n", false},
		{"still running", "=== recipe x ===\n(3/9) Installing foo\n", false},
		{"no log at all", "", false},
		{"the word done inside output", "installing done-stuff\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lastProvisionSucceeded(autoVM(t, "disk", nil, c.log)); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// TestAutoProvisionPromptNamesTheRecipes: "provision vm?" says nothing about
// what is about to run inside the guest, which is the whole point of asking.
func TestAutoProvisionPromptNamesTheRecipes(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh", "docker.alpine.sh"}, "")
	p := autoProvisionPrompt(v)
	for _, want := range []string{"vm", "xfce", "docker", "y/N"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt %q is missing %q", p, want)
		}
	}
	// Filenames are an implementation detail the user never chose.
	if strings.Contains(p, ".alpine.sh") {
		t.Errorf("prompt shows raw filenames: %q", p)
	}
}

// TestDeclinedOfferDoesNotOpenTheNewVMForm is the collision that makes a y/N
// prompt dangerous here: "n" is bound to new-VM, so declining with the obvious
// key would have opened the create form.
func TestDeclinedOfferDoesNotOpenTheNewVMForm(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh"}, "")
	for _, key := range []string{"n", "N", "esc", "q"} {
		m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
			provisioning: map[string]provState{}, pendingProvision: v}

		out, _ := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		after := out.(model)

		if after.screen != screenList {
			t.Errorf("declining with %q left screen %v", key, after.screen)
		}
		if after.pendingProvision != nil {
			t.Errorf("declining with %q left the offer pending", key)
		}
		if len(after.provisioning) != 0 {
			t.Errorf("declining with %q started provisioning anyway", key)
		}
		if !strings.Contains(after.toast.text, "press p") {
			t.Errorf("declining with %q gave no way back in: %q", key, after.toast.text)
		}
	}
}

// TestAcceptedOfferProvisions is the other half.
func TestAcceptedOfferProvisions(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh"}, "")
	m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
		provisioning: map[string]provState{}, pendingProvision: v}

	out, cmd := m.updateList(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	after := out.(model)

	if after.pendingProvision != nil {
		t.Error("accepting left the offer pending")
	}
	if _, running := after.provisioning[v.Name]; !running {
		t.Error("accepting did not start a provision run")
	}
	if cmd == nil {
		t.Error("accepting returned no command")
	}
}

// TestOfferNeverPreemptsADeletePrompt: a pending delete is a more
// consequential question and the user is mid-answer; an offer arriving on a
// 90-second timer must not overwrite it.
func TestOfferNeverPreemptsADeletePrompt(t *testing.T) {
	v := autoVM(t, "live", []string{"xfce.alpine.sh"}, "")
	m := model{screen: screenList, list: newVMList(), spin: newSpinner(),
		provisioning: map[string]provState{}, vms: []*config.VM{v},
		pendingDelete: v, status: "delete vm? y/N"}

	out, _ := m.Update(sshReadyMsg{name: v.Name})
	after := out.(model)

	if after.pendingProvision != nil {
		t.Error("the provision offer overwrote a pending delete confirmation")
	}
	if after.status != "delete vm? y/N" {
		t.Errorf("status = %q, want the delete prompt intact", after.status)
	}
}
