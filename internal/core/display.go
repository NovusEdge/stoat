package core

import (
	"fmt"

	"github.com/novusedge/stoat/internal/qemu"
)

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
// window could open on. Every function below takes it as an argument.
//
// GraphicalSession is re-exported for the same reason as the constants
// above: internal/cli does not import internal/qemu, and this is the one
// impure fact it needs. It costs an env read and at most one stat, cheap
// enough to call per command, and it is not cached: reading it where it is
// used means a user who exports the override sees it take effect.
func GraphicalSession() bool { return qemu.GraphicalSession() }

// Display is where a VM's screen is right now, and what to do about it.
//
// The answer changes without notice. A disk VM shows a real qemu window
// until setup-alpine finishes and stoat sets installed = true. Every start
// after that puts the screen on a VNC socket that neither the CLI nor the
// TUI mentions on its own. Users reported this as "doesn't spawn a qemu VM
// window anymore", with no error to go on.
type Display struct {
	// Kind is DisplayWindow or DisplayVNC, or "" for a broken VM, whose
	// vm.toml supplies neither of the facts the rule needs.
	Kind string
	// Socket is the VNC unix socket, empty unless Kind is DisplayVNC.
	Socket string
	// Attach is how to open Socket on this host. Zero unless Kind is
	// DisplayVNC.
	Attach qemu.Attach
	// NoSession is true when this VM wanted a window but the host had no
	// graphical session to open one on. It is the only case where Kind is
	// DisplayVNC for a reason about the host, not the VM. A caller uses it to
	// explain WHY there is no window: an OS installer on a VNC socket needs
	// explaining, or "no window" reads as a failure instead of a workaround.
	NoSession bool
}

// DisplayKind is the pure half: which surface, with no filesystem or PATH
// access. The JSON DTO calls this instead of DisplayFor, because a DTO
// constructor that checked PATH would do so once per VM in a list. graphical
// comes from GraphicalSession, resolved once by the caller for the same
// reason.
func DisplayKind(v VM, graphical bool) string {
	if v.State == StateBroken {
		return ""
	}
	return qemu.DisplayKind(v.Display, v.Mode, v.Installed, graphical)
}

// validateDisplay checks a config.VM.Display candidate. "" and "auto" both
// mean the installer-console default; anything but those two and "window"/
// "vnc" is rejected so a typo in vm.toml or an MCP call fails loudly instead
// of silently falling back to auto.
func validateDisplay(pref string) error {
	switch pref {
	case "", "auto", qemu.DisplayWindow, qemu.DisplayVNC:
		return nil
	}
	return fmt.Errorf("%w: display must be one of \"\", auto, window, vnc, not %q", ErrInvalidSpec, pref)
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
