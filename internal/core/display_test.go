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
	d := DisplayFor(v)
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
	d := DisplayFor(v)
	if d.Kind != DisplayWindow {
		t.Errorf("Kind = %q, want %q", d.Kind, DisplayWindow)
	}
	if d.Socket != "" || d.Attach.Command != "" {
		t.Errorf("a windowed VM must offer no VNC attach: %+v", d)
	}
}

// A broken vm.toml supplies neither mode nor installed, so the rule has
// nothing to run on. Guessing "vnc" would print a socket path for a VM that
// cannot start at all.
func TestDisplayForABrokenVMAnswersNothing(t *testing.T) {
	d := DisplayFor(VM{Name: "wreck", State: StateBroken, Error: "bad toml"})
	if d.Kind != "" {
		t.Errorf("Kind = %q, want empty for a broken VM", d.Kind)
	}
}

// DisplayKind is what the JSON DTO calls, once per VM in a list. It must not
// go near PATH: with PATH emptied it must still answer.
func TestDisplayKindIsPure(t *testing.T) {
	t.Setenv("PATH", "")
	if got := DisplayKind(VM{Mode: "cloud"}); got != DisplayVNC {
		t.Errorf("DisplayKind = %q, want %q", got, DisplayVNC)
	}
	if got := DisplayKind(VM{Mode: "disk"}); got != DisplayWindow {
		t.Errorf("DisplayKind = %q, want %q", got, DisplayWindow)
	}
}
