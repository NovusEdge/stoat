package core

import (
	"fmt"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
)

// ErrCapabilityUnavailable identifies an operation the VM's provider does
// not support. Match it with errors.Is; read the machine-readable reason by
// unwrapping to *CapabilityError with errors.As.
var ErrCapabilityUnavailable = capErr{}

type capErr struct{}

func (capErr) Error() string { return "capability unavailable" }

// CapabilityError carries why an operation was refused in fields, not only
// in its message. A JSON or MCP caller branches on Reason; parsing it back
// out of the sentence would make the wording a contract.
type CapabilityError struct {
	VM        string
	Operation string
	Reason    string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("capability unavailable: %s: %s (%s)", e.VM, e.Operation, e.Reason)
}

func (e *CapabilityError) Is(target error) bool { return target == ErrCapabilityUnavailable }

// RequireCapability refuses unless v's provider declares name as
// capabilities.StatusSupported. A provider that never declares the
// capability refuses too: silence is not permission.
func RequireCapability(v *config.VM, name string) error {
	p, err := providerFor(v)
	if err != nil {
		return err
	}
	for _, c := range p.Capabilities(v) {
		if c.Name != name {
			continue
		}
		if c.Status == capabilities.StatusSupported {
			return nil
		}
		reason := capabilities.ReasonProviderUnsupported
		if c.Reason != nil && c.Reason.Code != "" {
			reason = c.Reason.Code
		}
		return &CapabilityError{VM: v.Name, Operation: name, Reason: reason}
	}
	return &CapabilityError{VM: v.Name, Operation: name, Reason: "undeclared"}
}
