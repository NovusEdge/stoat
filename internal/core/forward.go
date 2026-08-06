package core

import (
	"fmt"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// PortForward is an alias, not a copy, of config.PortForward. This package
// otherwise gives every operation its own types (Spec vs config.VM) because
// those carry request-shaped fields config.VM does not (Start, Apply, Wait).
// A forward has no such extra shape: host port and guest port are the
// whole of it on both sides of the boundary, so duplicating the struct
// would only add a conversion step with nothing to convert.
type PortForward = config.PortForward

// Forward replaces VM name's declared port forwards and saves vm.toml.
//
// active reports whether they are in effect NOW. It is false when the VM is
// running: QEMU's user-mode netdev has no mechanism to hot-add a hostfwd rule
// to a live process, since the argv is fixed at launch, so a forward declared
// against a running VM is real (vm.toml has it, and it WILL apply) but inert
// until the next start. err is non-nil only when the forwards were NOT saved.
//
// This is a return value rather than a sentinel error on purpose. The design
// doc requires that "applied later" and "refused" never look the same to a
// caller (docs/design/core-api.md §8, decision 5), but encoding "saved, not
// yet live" as a non-nil error gets that exactly backwards in practice: every
// caller that writes the ordinary `if err != nil { return err }` would report
// a failure for an operation that succeeded. A contract needing an all-caps
// "callers MUST use errors.Is" to avoid being misread is the wrong shape. Here
// the common path cannot be got wrong: err means it did not happen, active
// means when.
//
// Validation is deliberately strict (see validateForwards) because every
// failure mode here is a qemu process that either refuses to start with an
// error naming no VM and no port, or two VMs silently fighting over one host
// listener. Both are far worse to debug after the fact than a rejected Forward
// call that names the exact offending port up front.
func Forward(name string, fwds []PortForward) (active bool, err error) {
	v, err := load(name)
	if err != nil {
		return false, err
	}
	if err := validateForwards(v, fwds); err != nil {
		return false, err
	}
	v.Forwards = fwds
	if err := v.Save(); err != nil {
		return false, err
	}
	return !qemu.Running(v), nil
}

// validateForwards checks a proposed forward list against everything that
// can make a qemu launch fail unhelpfully or two VMs collide on one host
// port. Every rule below is justified individually; none are speculative.
func validateForwards(v *config.VM, fwds []PortForward) error {
	seenInRequest := map[int]bool{}
	for _, f := range fwds {
		// Port 0 has meaning to net.Listen (kernel picks one) but none here:
		// stoat writes the port into vm.toml and qemu's hostfwd= syntax, and
		// neither has an "ask the kernel" mode. Negative and >65535 are not
		// TCP ports at all.
		if f.HostPort < 1 || f.HostPort > 65535 {
			return fmt.Errorf("%w: host port %d out of range (1-65535)", ErrInvalidSpec, f.HostPort)
		}
		if f.GuestPort < 1 || f.GuestPort > 65535 {
			return fmt.Errorf("%w: guest port %d out of range (1-65535)", ErrInvalidSpec, f.GuestPort)
		}

		// Ports below 1024 need CAP_NET_BIND_SERVICE (in practice: root) to
		// bind on Linux, which the user running stoat/qemu almost never
		// has. Left unchecked, this fails at qemu start with a bare
		// "Failed to bind socket: Permission denied" that names neither
		// the VM nor the port, the worst place to learn this, minutes
		// after Forward already reported success. Refusing here, with the
		// exact port named, beats "warn and let it fail invisibly later":
		// this API returns only an error, with no channel for a
		// non-fatal warning, so a warning would be silently dropped by
		// any caller that doesn't specifically look for it, indistinct
		// from not checking at all.
		if f.HostPort < 1024 {
			return fmt.Errorf("%w: host port %d needs root to bind; use 1024 or above", ErrInvalidSpec, f.HostPort)
		}

		if seenInRequest[f.HostPort] {
			return fmt.Errorf("%w: host port %d requested twice", ErrInvalidSpec, f.HostPort)
		}
		seenInRequest[f.HostPort] = true

		// A forward colliding with this VM's OWN ssh port is refused, not
		// merely redundant. Args renders every forward as another
		// hostfwd= clause on the SAME -netdev as the ssh one; two clauses
		// naming the same host port but different guest targets on one
		// netdev is not behaviour qemu documents, and relying on
		// "whichever one wins" is exactly the kind of silent, unexplained
		// failure this validation exists to prevent.
		if f.HostPort == v.SSHPort {
			return fmt.Errorf("%w: host port %d is this VM's own ssh port", ErrInvalidSpec, f.HostPort)
		}
	}

	// Collisions with ANOTHER VM's claimed ports are the same bug
	// config.FreePort and config.BrokenSSHPort already exist to prevent for
	// ssh ports: two qemu processes both trying to bind 127.0.0.1:<port>,
	// one losing at start time with no indication why. A user-declared
	// forward binds a host listener exactly like an ssh port does, so it
	// gets the same collision check, reusing FreePort's two data sources
	// (List, for parseable VMs; ListBroken+BrokenSSHPort, for VMs whose
	// vm.toml exists but a port is still committed to their disk) rather
	// than re-deriving "every port any VM has claimed" a second time.
	// claimant records who holds a port and how, so the refusal below can
	// name the colliding VM and say whether the collision is with that VM's
	// ssh port or one of its declared forwards. The fix differs (move THIS
	// port vs. change the OTHER vm's forward), so the message has to say
	// which.
	type claimant struct {
		vm   string
		kind string // "ssh port" or "declared forward"
	}
	claimed := map[int]claimant{}
	if vms, err := config.List(); err == nil {
		for _, other := range vms {
			// Identity is the directory, never the `name` field inside
			// vm.toml; see the identity note in vm.go. v.Dir is always
			// set here because it came from load(), which always sets it
			// via config.Load.
			if filepath.Base(other.Dir) == filepath.Base(v.Dir) {
				continue // this VM; its own port was already checked above
			}
			name := filepath.Base(other.Dir)
			claimed[other.SSHPort] = claimant{name, "ssh port"}
			for _, f := range other.Forwards {
				claimed[f.HostPort] = claimant{name, "declared forward"}
			}
		}
	}
	// A broken vm.toml can't be parsed for its Forwards, but FreePort's
	// existing regex-based BrokenSSHPort can still recover its ssh port
	// (the field most likely to still matter, since it's what a disk image
	// is committed to). Forwards on a broken VM are not recoverable this
	// way and are accepted as a known gap, matching FreePort's own.
	if broken, err := config.ListBroken(); err == nil {
		for _, b := range broken {
			if b.Name == filepath.Base(v.Dir) {
				continue
			}
			if p, err := config.BrokenSSHPort(b.Name); err == nil {
				claimed[p] = claimant{b.Name, "ssh port"}
			}
		}
	}
	for _, f := range fwds {
		if c, ok := claimed[f.HostPort]; ok {
			return fmt.Errorf("%w: host port %d is already used by vm %q's %s", ErrInvalidSpec, f.HostPort, c.vm, c.kind)
		}
	}
	return nil
}
