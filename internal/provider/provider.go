// Package provider owns the execution surface a VM runs on. internal/qemu is
// one surface; a cloud API is another. It sits below internal/core and must
// never import it: core maps a Status onto core.State, and the reverse
// dependency would close a cycle.
package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

// ErrUnknownProvider is For's answer for a vm.toml naming a provider this
// binary does not implement. It is never a fallback to qemu: a VM created by
// a newer stoat must not be operated as a local one.
var ErrUnknownProvider = errors.New("unknown provider")

// Status is what a provider knows about a machine. Raw carries the
// provider's own status word so a frontend can report what the API said;
// it is empty for a provider whose states already match Running.
type Status struct {
	Running   bool
	StartedAt time.Time
	Raw       string
}

// Provider is one execution surface. Endpoint is the boundary: everything
// above it reaches the guest over ssh and needs no provider knowledge.
type Provider interface {
	Name() string
	Capabilities(v *config.VM) []capabilities.Capability
	Start(ctx context.Context, v *config.VM) error
	Stop(ctx context.Context, v *config.VM) error
	Status(ctx context.Context, v *config.VM) (Status, error)
	Endpoint(ctx context.Context, v *config.VM) (sshx.Endpoint, error)

	// Create builds whatever the machine needs before it can start: a disk
	// image, a cloud instance. vm.toml is already written when this runs;
	// core deletes the VM's directory if Create returns an error, so a
	// failure still leaves no record behind, just not by running first.
	Create(ctx context.Context, v *config.VM) error

	// Destroy removes the machine. core deletes the VM record only after
	// this returns nil; the reverse order orphans a billing instance.
	Destroy(ctx context.Context, v *config.VM) error
}

// Details is what a cloud provider knows about a machine beyond Status: where
// it runs, its shape, and when it must stop. qemu has none of this, so it is
// a separate, optional interface rather than added to Provider's required
// surface.
type Details struct {
	Project      string
	Zone         string
	MachineType  string
	Address      string
	HardDeadline time.Time
	SoftDeadline time.Time
}

// Detailer is implemented by a Provider that can report Details. A caller
// type-asserts for it rather than assuming every Provider carries these
// facts.
type Detailer interface {
	Details(ctx context.Context, v *config.VM) (Details, error)
}

// WarnWithin is how far ahead of a deadline the CLI starts warning.
const WarnWithin = time.Hour

// Nearest picks whichever of a Details' two deadlines comes first. A zero
// time.Time means that deadline does not apply. which is the label the
// warning line names ("run-time limit" or "soft deadline").
func Nearest(hard, soft, now time.Time) (when time.Time, which string, ok bool) {
	switch {
	case hard.IsZero() && soft.IsZero():
		return time.Time{}, "", false
	case hard.IsZero():
		return soft, "soft deadline", true
	case soft.IsZero():
		return hard, "run-time limit", true
	case soft.Before(hard):
		return soft, "soft deadline", true
	default:
		return hard, "run-time limit", true
	}
}

var registry = map[string]Provider{}

// Register adds p under name. Implementations register from their own
// package's init, so importing an implementation is what makes it available.
func Register(name string, p Provider) { registry[name] = p }

// For resolves v's provider. An empty field means "qemu": every vm.toml
// written before the field existed describes a local QEMU VM.
func For(v *config.VM) (Provider, error) {
	name := v.Provider
	if name == "" {
		name = "qemu"
	}
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
	}
	return p, nil
}
