// Package backend turns guest.OS.Backend from a string into an interface.
// It is the only package allowed to assume "this is Alpine" or "this is
// cloud-init". internal/qemu no longer needs that assumption.
//
// This package cannot live inside internal/guest. internal/cloudinit,
// internal/recipes and internal/iso already import guest. An implementation
// here needs apkovl, cloudinit, recipes and keys, and importing those from
// guest would close an import cycle. guest stays a pure data leaf: OS facts
// only, zero imports. backend sits above it and pulls in what an
// implementation needs. See docs/design/guest-subsystem.md §3.2.
package backend

import (
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

// Backend is the behaviour a guest.OS.Backend name stands for: how to build
// the pre-boot artifact this OS needs, and what QEMU arguments that artifact
// requires. Only Prepare and Args are implemented here. Ready (today:
// sshx.Wait plus cloud-init polling) and Provision (today:
// internal/tui/provstep.go) are entangled with TUI code, and belong with the
// core-API work instead (docs/design/guest-subsystem.md §9).
type Backend interface {
	Name() string

	// Prepare builds the pre-boot artifact this backend needs, before QEMU
	// starts. Called on every start; each implementation decides for itself
	// whether v's current Mode/Installed state means there is anything to
	// do.
	Prepare(v *config.VM) error

	// Args contributes this backend's QEMU arguments: its own provisioning
	// artifact drive, or nothing. Pure: no filesystem access. It does NOT
	// contribute boot media (the qcow2, the installer -cdrom, -boot d).
	// That stays owned by qemu.Args's switch on Mode, because Mode and
	// Backend are different axes. A disk-mode install and a live boot, for
	// instance, use the same apkovl backend.
	Args(v *config.VM) []string
}

// noop is the "ssh" backend, and also what For returns for an OS not in the
// registry: no pre-boot artifact, no QEMU arguments. Nothing in today's
// registry declares "ssh" as its backend. A BYO image stoat can't recognise
// reaches noop the same way an ssh-backed OS does. That is the point: an
// unknown OS should behave like "provisioning happens over ssh, after the
// fact", not like a missing case.
type noop struct{}

func (noop) Name() string               { return "ssh" }
func (noop) Prepare(_ *config.VM) error { return nil }
func (noop) Args(_ *config.VM) []string { return nil }

// For returns the backend for v, or a no-op backend if none is known.
//
// v.Backend is the primary key, not v.OS. The form resolves it once at
// creation time from the chosen catalog entry. A catalog entry can override
// its OS's usual backend: "alpine-cloud" has OS "alpine" but Backend
// "cloudinit", because that image boots off a cloud-init seed like any
// other cloud image. guest.Lookup(v.OS) is only a fallback for a VM whose
// Backend field was never populated. It must never run ahead of an
// explicit v.Backend, or alpine-cloud gets the wrong backend.
func For(v *config.VM) Backend {
	name := v.Backend
	if name == "" {
		if o, ok := guest.Lookup(v.OS); ok {
			name = o.Backend
		}
	}
	switch name {
	case "apkovl":
		return apkovlBackend{}
	case "cloudinit":
		return cloudinitBackend{}
	default:
		return noop{}
	}
}
