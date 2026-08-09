package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// TestListShowsProvisionProgressBar proves a recipe run with a parsed
// package-progress line renders the bar and package name in the list row,
// replacing the raw last-output text that provLine falls back to otherwise.
func TestListShowsProvisionProgressBar(t *testing.T) {
	v := core.VM{Name: "prov-vm", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}
	m := model{
		screen: screenList, width: 100, height: 40,
		vms:  []core.VM{v},
		spin: newSpinner(),
		provisioning: map[string]provState{
			"prov-vm": {
				start: time.Now(), step: "xfce.alpine.sh", last: "(50/200) Installing bash-5.2-r0",
				prog: Progress{Done: 50, Total: 200, Frac: 0.25, Label: "bash-5.2-r0"}, hasProg: true,
			},
		},
	}
	m.list = newVMList()

	out := ansi.Strip(m.viewList())
	if !strings.Contains(out, "50/200") {
		t.Errorf("list view missing the package count:\n%s", out)
	}
	if !strings.Contains(out, "bash-5.2-r0") {
		t.Errorf("list view missing the package name:\n%s", out)
	}
	if strings.Contains(out, "(50/200) Installing bash-5.2-r0") {
		t.Errorf("list view should render the bar in place of the raw log line, not both:\n%s", out)
	}
}

// TestListShowsInstallProgressBar proves an uninstalled disk VM that is
// running gets its own list row, built from the console log rather than a
// tracked provisioning entry, since the install starts itself without ever
// going through startProvision.
func TestListShowsInstallProgressBar(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	cv := &config.VM{Name: "installing-vm", Mode: "disk", Disk: "8G"}
	if err := cv.Save(); err != nil {
		t.Fatalf("saving fixture vm.toml: %v", err)
	}
	if err := os.WriteFile(cv.ConsoleLogPath(), []byte("(3/50) Installing musl-1.2.5-r0\n"), 0o644); err != nil {
		t.Fatalf("writing console.log: %v", err)
	}

	v := core.VM{
		Name: cv.Name, Mode: "disk", Installed: false, State: core.StateRunning,
		StartedAt: time.Now().Add(-30 * time.Second),
		Paths:     core.Paths{Dir: cv.Dir},
	}
	m := model{
		screen: screenList, width: 100, height: 40,
		vms: []core.VM{v}, spin: newSpinner(), provisioning: map[string]provState{},
	}
	m.list = newVMList()

	out := ansi.Strip(m.viewList())
	if !strings.Contains(out, "installing") {
		t.Errorf("list view missing the install phase label:\n%s", out)
	}
	if !strings.Contains(out, "3/50") {
		t.Errorf("list view missing the package count:\n%s", out)
	}
	if !strings.Contains(out, "musl-1.2.5-r0") {
		t.Errorf("list view missing the package name:\n%s", out)
	}
}

// TestDetailShowsProgressPanelForProvisioning proves the detail screen draws
// a fuller panel for the selected VM's own recipe run: phase, bar, package,
// elapsed, all in one pane rather than the list's compact line.
func TestDetailShowsProgressPanelForProvisioning(t *testing.T) {
	v := core.VM{Name: "detail-prov", Mode: "live", Paths: core.Paths{Dir: t.TempDir()}}
	m := model{
		screen: screenDetail, width: 100, height: 40,
		spin: newSpinner(),
		provisioning: map[string]provState{
			"detail-prov": {
				start: time.Now().Add(-45 * time.Second), step: "docker.alpine.sh",
				prog: Progress{Done: 12, Total: 40, Frac: 0.3, Label: "docker-cli-24.0-r1"}, hasProg: true,
			},
		},
	}
	m.detail = newDetail(v)

	out := ansi.Strip(m.viewDetail())
	if !strings.Contains(out, "docker.alpine.sh") {
		t.Errorf("detail panel missing the recipe phase:\n%s", out)
	}
	if !strings.Contains(out, "docker-cli-24.0-r1") {
		t.Errorf("detail panel missing the package name:\n%s", out)
	}
	if !strings.Contains(out, "12/40") {
		t.Errorf("detail panel missing the package count:\n%s", out)
	}
}

// TestDetailShowsProgressPanelForInstalling proves the detail screen draws
// the same panel for an uninstalled disk VM mid unattended install, reading
// the console log the way progressPanel derives it, with no tracked
// provState behind it.
func TestDetailShowsProgressPanelForInstalling(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	cv := &config.VM{Name: "detail-installing", Mode: "disk", Disk: "8G"}
	if err := cv.Save(); err != nil {
		t.Fatalf("saving fixture vm.toml: %v", err)
	}
	if err := os.WriteFile(cv.ConsoleLogPath(), []byte("67% ##########################\n"), 0o644); err != nil {
		t.Fatalf("writing console.log: %v", err)
	}

	v := core.VM{
		Name: cv.Name, Mode: "disk", Installed: false, State: core.StateRunning,
		StartedAt: time.Now().Add(-90 * time.Second),
		Paths:     core.Paths{Dir: cv.Dir},
	}
	m := model{screen: screenDetail, width: 100, height: 40, spin: newSpinner(), provisioning: map[string]provState{}}
	m.detail = newDetail(v)

	out := ansi.Strip(m.viewDetail())
	if !strings.Contains(out, "installing") {
		t.Errorf("detail panel missing the install phase:\n%s", out)
	}
	if !strings.Contains(out, "67%") {
		t.Errorf("detail panel missing the percent:\n%s", out)
	}
}
