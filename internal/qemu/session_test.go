package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headless clears every variable GraphicalSession looks at and points
// XDG_RUNTIME_DIR at an empty directory, which is what a real headless host
// looks like: the directory usually exists (systemd makes one per login), it
// just has no compositor socket in it.
//
// Clearing DISPLAY and WAYLAND_DISPLAY alone would NOT be headless, which is
// the trap this whole check exists for: GTK finds $XDG_RUNTIME_DIR/wayland-0
// by itself and starts. A test that faked headless that way would be asserting
// nothing, and verified against qemu it is the difference between exit 1 and a
// VM that runs.
func headless(t *testing.T) string {
	t.Helper()
	rt := t.TempDir()
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv(GraphicalEnv, "")
	return rt
}

func TestGraphicalSessionOnAHostWithASession(t *testing.T) {
	// X11.
	headless(t)
	t.Setenv("DISPLAY", ":0")
	if !GraphicalSession() {
		t.Error("DISPLAY is set: that is a session")
	}

	// Wayland, named.
	headless(t)
	t.Setenv("WAYLAND_DISPLAY", "wayland-1")
	if !GraphicalSession() {
		t.Error("WAYLAND_DISPLAY is set: that is a session")
	}

	// Wayland, unnamed: neither variable is set, and there is a session
	// anyway. This is the case that produced a wrong measurement, and the
	// reason this function does not stop at the two obvious variables.
	rt := headless(t)
	if err := os.WriteFile(filepath.Join(rt, "wayland-0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !GraphicalSession() {
		t.Error("$XDG_RUNTIME_DIR/wayland-0 exists: GTK finds it, so stoat must too")
	}
}

func TestGraphicalSessionOnATrulyHeadlessHost(t *testing.T) {
	headless(t)
	if GraphicalSession() {
		t.Error("no DISPLAY, no WAYLAND_DISPLAY and no wayland socket is headless")
	}

	// A compositor on wayland-1 with WAYLAND_DISPLAY unset is unreachable:
	// GTK only guesses wayland-0. Claiming a session here would hand qemu a
	// window it then fails to open.
	rt := headless(t)
	if err := os.WriteFile(filepath.Join(rt, "wayland-1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if GraphicalSession() {
		t.Error("wayland-1 with no WAYLAND_DISPLAY is not something GTK can find")
	}

	// No XDG_RUNTIME_DIR at all: nothing left to look in.
	headless(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	if GraphicalSession() {
		t.Error("no runtime dir and no variables is headless")
	}
}

// The override is the escape hatch for both directions of a wrong inference:
// a session this does not recognize, and a session qemu cannot actually draw
// on (a GTK build with no working GL, which no environment check can see).
func TestGraphicalSessionOverrideWinsBothWays(t *testing.T) {
	rt := headless(t)
	if err := os.WriteFile(filepath.Join(rt, "wayland-0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv(GraphicalEnv, "0")
	if GraphicalSession() {
		t.Errorf("%s=0 must force the VNC path even with a session present", GraphicalEnv)
	}

	headless(t)
	t.Setenv(GraphicalEnv, "1")
	if !GraphicalSession() {
		t.Errorf("%s=1 must force the window even with nothing detected", GraphicalEnv)
	}

	// A value that is not a bool is not an instruction. Falling back to
	// detection beats refusing to start a VM over a typo in an env var.
	headless(t)
	t.Setenv(GraphicalEnv, "yes please")
	if GraphicalSession() {
		t.Error("an unparseable override must fall through to detection, not be read as true")
	}
}

// The message a user actually reads when qemu refuses the window. "OpenGL is
// not supported" sends them to mesa and GPU drivers; the cause is the display,
// and there is a workaround, so both have to be in the text.
func TestExplainDisplayFailureNamesTheRealCause(t *testing.T) {
	for _, msg := range []string{
		"qemu-system-x86_64: OpenGL is not supported by display backend 'gtk'",
		"gtk initialization failed",
	} {
		got := explainDisplayFailure(msg)
		if !strings.Contains(got, "not your GPU") {
			t.Errorf("explanation for %q must contradict the OpenGL reading: %q", msg, got)
		}
		if !strings.Contains(got, GraphicalEnv+"=0") {
			t.Errorf("explanation for %q must name the way out: %q", msg, got)
		}
	}
	// Every other start failure is left exactly as qemu reported it.
	if got := explainDisplayFailure("Could not access KVM kernel module"); got != "" {
		t.Errorf("an unrelated failure must not get a display explanation: %q", got)
	}
}
