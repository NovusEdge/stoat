package core

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// PortForward is an alias, not a copy, of config.PortForward. This package
// gives most operations their own types (Spec vs config.VM) because those
// carry request-shaped fields config.VM lacks (Start, Apply, Wait). A
// forward has no extra shape: host port and guest port are the whole of it
// on both sides. Duplicating the struct would only add a conversion step.
type PortForward = config.PortForward

// ParseForwards reads "8080:80" pairs, host port first: the spelling docker
// and ssh -L both use, so the ordering is the one a user already has in
// their fingers. Getting it backwards silently binds the wrong port, so the
// error names the whole offending argument rather than just complaining
// about a number.
func ParseForwards(pairs []string) ([]PortForward, error) {
	var out []PortForward
	for _, p := range pairs {
		host, guest, ok := strings.Cut(p, ":")
		if !ok {
			return nil, fmt.Errorf("%q is not a HOST:GUEST port pair", p)
		}
		h, err := strconv.Atoi(strings.TrimSpace(host))
		if err != nil {
			return nil, fmt.Errorf("%q: host port %q is not a number", p, host)
		}
		g, err := strconv.Atoi(strings.TrimSpace(guest))
		if err != nil {
			return nil, fmt.Errorf("%q: guest port %q is not a number", p, guest)
		}
		out = append(out, PortForward{HostPort: h, GuestPort: g})
	}
	return out, nil
}

// Forward replaces VM name's declared port forwards and saves vm.toml.
//
// active reports whether the forwards are in effect now. It is false when
// the VM is running: QEMU's user-mode netdev cannot hot-add a hostfwd rule
// to a live process, since argv is fixed at launch. A forward declared
// against a running VM is saved and will apply, but stays inert until the
// next start. err is non-nil only when the forwards were not saved.
//
// active is a return value, not a sentinel error, so "saved, not yet live"
// and "refused" cannot be confused (docs/design/core-api.md §8, decision
// 5). A caller writing the ordinary `if err != nil { return err }` gets
// the right answer either way: err means it did not happen, active means
// when.
//
// Validation is strict; see validateForwards. Every failure mode here is a
// qemu process that refuses to start, or two VMs fighting over one host
// listener. Both are harder to debug after the fact than a Forward call
// that names the offending port up front.
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
		// Port 0 means "kernel picks one" to net.Listen, but not here:
		// stoat writes the port into vm.toml and qemu's hostfwd= syntax,
		// and neither has that mode. Negative ports and ports above
		// 65535 are not TCP ports.
		if f.HostPort < 1 || f.HostPort > 65535 {
			return fmt.Errorf("%w: host port %d out of range (1-65535)", ErrInvalidSpec, f.HostPort)
		}
		if f.GuestPort < 1 || f.GuestPort > 65535 {
			return fmt.Errorf("%w: guest port %d out of range (1-65535)", ErrInvalidSpec, f.GuestPort)
		}

		// Ports below 1024 need CAP_NET_BIND_SERVICE, in practice root,
		// to bind on Linux. The user running stoat/qemu almost never has
		// it. Left unchecked, this fails at qemu start with "Failed to
		// bind socket: Permission denied", an error naming neither the
		// VM nor the port, minutes after Forward already reported
		// success. This API has no channel for a non-fatal warning, so
		// refusing here, with the exact port named, is the only way to
		// surface the problem.
		if f.HostPort < 1024 {
			return fmt.Errorf("%w: host port %d needs root to bind; use 1024 or above", ErrInvalidSpec, f.HostPort)
		}

		if seenInRequest[f.HostPort] {
			return fmt.Errorf("%w: host port %d requested twice", ErrInvalidSpec, f.HostPort)
		}
		seenInRequest[f.HostPort] = true

		// A forward colliding with this VM's own ssh port is refused,
		// not merely redundant. Args renders every forward as another
		// hostfwd= clause on the same -netdev as the ssh one. qemu does
		// not document what happens when two clauses name the same host
		// port with different guest targets, and relying on "whichever
		// one wins" is the silent failure this validation exists to
		// prevent.
		if f.HostPort == v.SSHPort {
			return fmt.Errorf("%w: host port %d is this VM's own ssh port", ErrInvalidSpec, f.HostPort)
		}
	}

	// A collision with another VM's claimed port is the same bug FreePort
	// and BrokenSSHPort already prevent for ssh ports: two qemu processes
	// binding 127.0.0.1:<port>, one losing at start with no indication why.
	// A declared forward binds a host listener exactly like an ssh port, so
	// it gets the same check. This reuses FreePort's two data sources: List
	// for parseable VMs, ListBroken plus BrokenSSHPort for VMs whose
	// vm.toml exists but still commits a port to disk.
	//
	// claimant records who holds a port and how, so the refusal below can
	// name the colliding VM and say whether the collision is with its ssh
	// port or a declared forward. The fix differs: move this port, or
	// change the other VM's forward. The message must say which.
	type claimant struct {
		vm   string
		kind string // "ssh port" or "declared forward"
	}
	claimed := map[int]claimant{}
	if vms, err := config.List(); err == nil {
		for _, other := range vms {
			// Identity is the directory, never vm.toml's `name` field; see
			// the identity note in vm.go. v.Dir is always set here: it came
			// from load(), which sets it via config.Load.
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
	// A broken vm.toml can't be parsed for its Forwards, but BrokenSSHPort
	// can still recover its ssh port by regex, the field a disk image is
	// actually committed to. Forwards on a broken VM are not recoverable
	// this way; that gap is accepted, matching FreePort's own.
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
