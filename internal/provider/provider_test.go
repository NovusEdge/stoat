package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

type stub struct{ name string }

func (s stub) Name() string                                    { return s.name }
func (stub) Capabilities(*config.VM) []capabilities.Capability { return nil }
func (stub) Start(context.Context, *config.VM) error           { return nil }
func (stub) Stop(context.Context, *config.VM) error            { return nil }
func (stub) Status(context.Context, *config.VM) (Status, error) {
	return Status{Running: true, StartedAt: time.Unix(1, 0)}, nil
}
func (stub) Endpoint(context.Context, *config.VM) (sshx.Endpoint, error) {
	return sshx.Endpoint{}, nil
}

func TestForDefaultsToQemuWhenUnset(t *testing.T) {
	Register("qemu", stub{name: "qemu"})
	p, err := For(&config.VM{Name: "dev"})
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if p.Name() != "qemu" {
		t.Errorf("Name() = %q, want qemu for a VM with no provider field", p.Name())
	}
}

func TestForRejectsUnknownProvider(t *testing.T) {
	_, err := For(&config.VM{Name: "dev", Provider: "nope"})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("For() error = %v, want ErrUnknownProvider", err)
	}
}
