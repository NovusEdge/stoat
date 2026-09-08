package core

import (
	"errors"
	"fmt"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
)

// ErrCapabilityUnavailable identifies an operation the VM's provider does
// not support, wrapping the capability's Reason so a caller can report why.
var ErrCapabilityUnavailable = errors.New("capability unavailable")

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
		reason := ""
		if c.Reason != nil {
			reason = c.Reason.Code
		}
		return fmt.Errorf("%w: %s: %s (%s)", ErrCapabilityUnavailable, v.Name, name, reason)
	}
	return fmt.Errorf("%w: %s: %s (undeclared)", ErrCapabilityUnavailable, v.Name, name)
}
