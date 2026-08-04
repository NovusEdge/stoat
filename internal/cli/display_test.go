package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// fakeViewers makes PATH contain exactly the named binaries, so the attach
// command a test asserts on does not depend on what this host has installed.
//
// ssh-keygen is linked in alongside them: Main generates the data root's
// keypair on startup (internal/keys), and a PATH without it fails every
// command before it reaches the display line under test.
func fakeViewers(t *testing.T, names ...string) {
	t.Helper()
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	if err := os.Symlink(keygen, filepath.Join(dir, "ssh-keygen")); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func getText(t *testing.T, name string) string {
	t.Helper()
	var out, errOut strings.Builder
	if code := Main([]string{"get", name}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	return out.String()
}

// The reported bug: a disk VM whose window vanishes once setup-alpine marks
// it installed, with nothing anywhere saying where the screen went.
func TestGetTellsAnInstalledDiskVMWhereItsScreenWent(t *testing.T) {
	root := cliRoot(t)
	saveVM(t, &config.VM{Name: "alpinedisk", OS: "alpine", Mode: "disk", Disk: "8G",
		Installed: true, RAM: 2048, CPUs: 2, SSHPort: 2200})
	fakeViewers(t, "gvncviewer")

	out := getText(t, "alpinedisk")
	sock := filepath.Join(root, "alpinedisk", "vnc.sock")
	for _, want := range []string{"display: no qemu window", sock, "attach with: gvncviewer " + sock} {
		if !strings.Contains(out, want) {
			t.Errorf("get output missing %q:\n%s", want, out)
		}
	}
}

// Before the install finishes there IS a window, and saying "no qemu window"
// then would send a user hunting for a socket qemu never bound.
func TestGetSaysAFreshDiskVMHasAWindow(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "alpinedisk", OS: "alpine", Mode: "disk", Disk: "8G",
		Installed: false, RAM: 2048, CPUs: 2, SSHPort: 2200})

	out := getText(t, "alpinedisk")
	if !strings.Contains(out, "display: a qemu window") {
		t.Errorf("get output does not mention the install window:\n%s", out)
	}
	if strings.Contains(out, "vnc.sock") {
		t.Errorf("a windowed VM must not be sent to a VNC socket:\n%s", out)
	}
}

// Printing a command for a binary that is not installed is worse than saying
// nothing: it reads as an instruction and fails as one.
func TestGetNamesWhatToInstallWhenNoViewerExists(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "cloudy", OS: "alpine", Mode: "cloud",
		RAM: 2048, CPUs: 2, SSHPort: 2201})
	fakeViewers(t)

	out := getText(t, "cloudy")
	if strings.Contains(out, "attach with:") {
		t.Errorf("no viewer is installed, so no command may be printed:\n%s", out)
	}
	if !strings.Contains(out, "no VNC viewer found; install one of: gvncviewer, socat") {
		t.Errorf("output does not say what to install:\n%s", out)
	}
}

// The socat path is two steps and the second one is the half that shows
// anybody anything.
func TestGetPrintsTheBridgeFollowUpStep(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "cloudy", OS: "alpine", Mode: "cloud",
		RAM: 2048, CPUs: 2, SSHPort: 2201})
	fakeViewers(t, "socat")

	out := getText(t, "cloudy")
	if !strings.Contains(out, "attach with: socat ") || !strings.Contains(out, "127.0.0.1:5900") {
		t.Errorf("output does not describe the bridge and where to connect:\n%s", out)
	}
}

// A broken vm.toml supplies neither mode nor installed, so there is no
// honest answer and get must not invent one.
func TestGetOnABrokenVMSaysNothingAboutTheDisplay(t *testing.T) {
	root := cliRoot(t)
	writeBrokenVMToml(t, root, "wreck")

	out := getText(t, "wreck")
	if strings.Contains(out, "display:") {
		t.Errorf("broken VM got a display line:\n%s", out)
	}
}

// --json carries WHICH surface and never WHERE: the socket is an absolute
// host path, and docs/reference/json.md keeps those off the wire.
func TestJSONCarriesTheDisplayKindButNeverTheSocket(t *testing.T) {
	root := cliRoot(t)
	saveVM(t, &config.VM{Name: "alpinedisk", OS: "alpine", Mode: "disk", Disk: "8G",
		Installed: true, RAM: 2048, CPUs: 2, SSHPort: 2200})
	fakeViewers(t, "gvncviewer")

	code, objs := runJSON(t, "get", "alpinedisk")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	vm, _ := dataOf(t, objs)["vm"].(map[string]any)
	if vm["display"] != core.DisplayVNC {
		t.Errorf("display = %v, want %q", vm["display"], core.DisplayVNC)
	}
	b, err := json.Marshal(objs)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{root, "vnc.sock", "gvncviewer"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("JSON leaks %q:\n%s", bad, b)
		}
	}
}

// The rule the DTO publishes must be the rule qemu.Args actually applies, or
// a consumer is told about a surface the VM does not have.
func TestJSONDisplayIsWindowBeforeTheInstallFinishes(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "alpinedisk", OS: "alpine", Mode: "disk", Disk: "8G",
		Installed: false, RAM: 2048, CPUs: 2, SSHPort: 2200})

	code, objs := runJSON(t, "get", "alpinedisk")
	if code != ExitOK {
		t.Fatalf("exit = %d: %v", code, objs)
	}
	vm, _ := dataOf(t, objs)["vm"].(map[string]any)
	if vm["display"] != core.DisplayWindow {
		t.Errorf("display = %v, want %q", vm["display"], core.DisplayWindow)
	}
}
