package core

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeBins(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// The reported case: a disk VM that showed a window through setup-alpine and
// is headless on every start after it. DisplayFor must be able to say where
// the screen went and what opens it.
func TestDisplayForAnInstalledDiskVM(t *testing.T) {
	fakeBins(t, "gvncviewer")
	v := VM{
		Name: "alpinedisk", Mode: "disk", Installed: true, State: StateStopped,
		Paths: Paths{VNCSocket: "/home/u/.stoat/alpinedisk/vnc.sock"},
	}
	d := DisplayFor(v, true)
	if d.Kind != DisplayVNC {
		t.Errorf("Kind = %q, want %q", d.Kind, DisplayVNC)
	}
	if d.Socket != v.Paths.VNCSocket {
		t.Errorf("Socket = %q", d.Socket)
	}
	if d.Attach.Command == "" {
		t.Error("a viewer is installed, so an attach command must be offered")
	}
}

// The install console. It gets a real window, so there is no socket to offer
// and offering one would send a user to a path qemu never bound.
func TestDisplayForAFreshDiskVMIsAWindow(t *testing.T) {
	v := VM{
		Name: "alpinedisk", Mode: "disk", Installed: false, State: StateStopped,
		Paths: Paths{VNCSocket: "/home/u/.stoat/alpinedisk/vnc.sock"},
	}
	d := DisplayFor(v, true)
	if d.Kind != DisplayWindow {
		t.Errorf("Kind = %q, want %q", d.Kind, DisplayWindow)
	}
	if d.Socket != "" || d.Attach.Command != "" {
		t.Errorf("a windowed VM must offer no VNC attach: %+v", d)
	}
	if d.NoSession {
		t.Error("NoSession must be false when the host had a session and the VM got its window")
	}
}

// The same VM on a host with no graphical session. It cannot have the window,
// and before the fallback existed it could not start at all, so it gets the
// socket plus the flag that says this is the host's doing and not the VM's.
func TestDisplayForAFreshDiskVMWithNoSessionFallsBackToVNC(t *testing.T) {
	fakeBins(t, "gvncviewer")
	v := VM{
		Name: "alpinedisk", Mode: "disk", Installed: false, State: StateStopped,
		Paths: Paths{VNCSocket: "/home/u/.stoat/alpinedisk/vnc.sock"},
	}
	d := DisplayFor(v, false)
	if d.Kind != DisplayVNC {
		t.Errorf("Kind = %q, want %q", d.Kind, DisplayVNC)
	}
	if !d.NoSession {
		t.Error("NoSession must say why there is no window, or the user reads this as the failure")
	}
	if d.Attach.Command == "" {
		t.Error("the install has to be drivable from elsewhere, so an attach command is the whole point")
	}
}

// An installed disk VM was already on VNC for its own reasons. A headless host
// must not make it claim it was demoted, or the explanation gets printed on
// every VM stoat has.
func TestDisplayForAnAlreadyHeadlessVMIsNotBlamedOnTheHost(t *testing.T) {
	fakeBins(t)
	v := VM{Name: "c", Mode: "cloud", State: StateStopped, Paths: Paths{VNCSocket: "/x/vnc.sock"}}
	if d := DisplayFor(v, false); d.NoSession {
		t.Error("a cloud VM never wanted a window; NoSession must be false")
	}
}

// A broken vm.toml supplies neither mode nor installed, so the rule has
// nothing to run on. Guessing "vnc" would print a socket path for a VM that
// cannot start at all.
func TestDisplayForABrokenVMAnswersNothing(t *testing.T) {
	d := DisplayFor(VM{Name: "wreck", State: StateBroken, Error: "bad toml"}, true)
	if d.Kind != "" {
		t.Errorf("Kind = %q, want empty for a broken VM", d.Kind)
	}
}

// DisplayKind is what the JSON DTO calls, once per VM in a list. It must not
// go near PATH or the environment. With PATH emptied and every display
// variable cleared, it must still answer from its arguments alone, so the
// wire value cannot depend on the machine the test runs on.
func TestDisplayKindIsPure(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := DisplayKind(VM{Mode: "cloud"}, true); got != DisplayVNC {
		t.Errorf("DisplayKind = %q, want %q", got, DisplayVNC)
	}
	if got := DisplayKind(VM{Mode: "disk"}, true); got != DisplayWindow {
		t.Errorf("DisplayKind = %q, want %q", got, DisplayWindow)
	}
	// ...and the host's veto is carried, not re-derived.
	if got := DisplayKind(VM{Mode: "disk"}, false); got != DisplayVNC {
		t.Errorf("DisplayKind = %q, want %q on a host with no session", got, DisplayVNC)
	}
}
