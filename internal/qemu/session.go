package qemu

import (
	"os"
	"path/filepath"
	"strconv"
)

// GraphicalEnv forces the answer GraphicalSession would otherwise infer.
//
// An env var rather than a flag: the same fact is needed by `stoat up`, by the
// TUI and by anything driving the CLI over ssh, and only one of those three has
// a flag surface to put it on. It also has to be settable for a whole session
// (`export STOAT_GRAPHICAL=0`) by someone who already knows their host better
// than this inference does.
//
// Both directions are useful. STOAT_GRAPHICAL=0 forces the VNC path on a host
// that has a session qemu cannot actually draw on: a GTK build without working
// GL fails on the gl=on option before it ever tries to open a window, and no
// environment check can see that coming. STOAT_GRAPHICAL=1 forces the window
// back on a session this detection does not recognize.
const GraphicalEnv = "STOAT_GRAPHICAL"

// GraphicalSession reports whether this host has a display server that a
// `-display gtk` qemu could open a window on.
//
// Impure by nature (env plus one stat), which is why it is here and not in the
// display rule it feeds: DisplayKind and Args are pure and unit tested on that
// basis, so the answer is resolved once, up here, and passed down.
//
// The X11 and Wayland variables are not the whole test. GTK falls back to
// $XDG_RUNTIME_DIR/wayland-0 when WAYLAND_DISPLAY is unset, so a "headless"
// shell with both variables cleared still opens a window.
//
// wayland-0 exactly, not a wayland-* glob: a compositor on wayland-1 is only
// reachable through WAYLAND_DISPLAY, which is already checked above it, and
// guessing otherwise would claim a session qemu then fails to find.
func GraphicalSession() bool {
	if v, err := strconv.ParseBool(os.Getenv(GraphicalEnv)); err == nil {
		return v
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != "" {
		return true
	}
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(rt, "wayland-0"))
	return err == nil
}
