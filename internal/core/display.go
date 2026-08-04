package core

import "github.com/novusedge/stoat/internal/qemu"

// Re-exported so a caller can branch on Display.Kind without importing
// internal/qemu, which is otherwise entirely below this package.
const (
	DisplayWindow = qemu.DisplayWindow
	DisplayVNC    = qemu.DisplayVNC
)

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
}

// DisplayKind is the pure half: which surface, with no filesystem or PATH
// access. The JSON DTO uses this rather than DisplayFor, because a DTO
// constructor that shelled out to look at PATH would do so once per VM in a
// list.
func DisplayKind(v VM) string {
	if v.State == StateBroken {
		return ""
	}
	return qemu.DisplayKind(v.Mode, v.Installed)
}

// DisplayFor is DisplayKind plus the lookup of a VNC viewer installed on this
// host, for the callers that render an attach command for a human.
func DisplayFor(v VM) Display {
	d := Display{Kind: DisplayKind(v)}
	if d.Kind == DisplayVNC {
		d.Socket = v.Paths.VNCSocket
		d.Attach = qemu.AttachVNC(d.Socket)
	}
	return d
}
