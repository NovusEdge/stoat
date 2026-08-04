package core

import "github.com/novusedge/stoat/internal/qemu"

// Re-exported so a caller can branch on Display.Kind without importing
// internal/qemu, which is otherwise entirely below this package.
const (
	DisplayWindow = qemu.DisplayWindow
	DisplayVNC    = qemu.DisplayVNC
	// GraphicalEnv overrides what GraphicalSession would infer. Re-exported
	// so a caller can name it in its own message, or pin it in a test, without
	// reaching past this package for the string.
	GraphicalEnv = qemu.GraphicalEnv
)

// GraphicalSession reports whether this host has a display server a qemu
// window could open on, and is the argument every function below wants.
//
// Re-exported for the same reason the constants above are: internal/cli
// deliberately does not import internal/qemu, and this is the one impure fact
// it has to supply. Cheap enough to call per command (an env read and at most
// one stat), and deliberately not cached: it is read where it is used, so a
// user who exports the override sees it take effect.
func GraphicalSession() bool { return qemu.GraphicalSession() }

// Display is where a VM's screen is right now, and what to do about it.
//
// It exists because the answer changes under the user without saying so: a
// disk VM shows a real qemu window until setup-alpine finishes and stoat sets
// installed = true, and every start after that puts the screen on a VNC
// socket that nothing in the CLI or the TUI mentions. Reported as "doesn't
// spawn a qemu VM window anymore", with no error to go on.
type Display struct {
	// Kind is DisplayWindow or DisplayVNC, or "" for a broken VM, whose
	// vm.toml supplies neither of the facts the rule needs.
	Kind string
	// Socket is the VNC unix socket, empty unless Kind is DisplayVNC.
	Socket string
	// Attach is how to open Socket on this host. Zero unless Kind is
	// DisplayVNC.
	Attach qemu.Attach
	// NoSession is true when this VM wanted a window and the host had no
	// graphical session to open one on, which is the only case where Kind is
	// DisplayVNC for a reason that is about the host rather than the VM. It
	// exists so a caller can say WHY there is no window: an OS installer on a
	// VNC socket needs explaining, and "no window" on its own reads as the
	// failure the user just hit rather than the workaround for it.
	NoSession bool
}

// DisplayKind is the pure half: which surface, with no filesystem or PATH
// access. The JSON DTO uses this rather than DisplayFor, because a DTO
// constructor that shelled out to look at PATH would do so once per VM in a
// list. graphical comes from GraphicalSession, resolved once by the caller for
// the same reason.
func DisplayKind(v VM, graphical bool) string {
	if v.State == StateBroken {
		return ""
	}
	return qemu.DisplayKind(v.Mode, v.Installed, graphical)
}

// DisplayFor is DisplayKind plus the lookup of a VNC viewer installed on this
// host, for the callers that render an attach command for a human.
func DisplayFor(v VM, graphical bool) Display {
	d := Display{Kind: DisplayKind(v, graphical)}
	if d.Kind == DisplayVNC {
		d.Socket = v.Paths.VNCSocket
		d.Attach = qemu.AttachVNC(d.Socket)
		d.NoSession = !graphical && DisplayKind(v, true) == DisplayWindow
	}
	return d
}
