// Package qemu implements provider.Provider over internal/qemu, the local
// hypervisor. It holds no logic of its own: every call forwards.
package qemu

import (
	"context"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

func init() { provider.Register("qemu", Provider{}) }

type Provider struct{}

func (Provider) Name() string { return "qemu" }

// Capabilities returns nothing. The capability set still lives in
// internal/capabilities and moves here with the code that reads it.
func (Provider) Capabilities(*config.VM) []capabilities.Capability { return nil }

func (Provider) Start(_ context.Context, v *config.VM) error { return qemu.Start(v) }

func (Provider) Stop(_ context.Context, v *config.VM) error { return qemu.Stop(v) }

func (Provider) Status(_ context.Context, v *config.VM) (provider.Status, error) {
	return provider.Status{Running: qemu.Running(v), StartedAt: qemu.StartedAt(v)}, nil
}

func (Provider) Endpoint(_ context.Context, v *config.VM) (sshx.Endpoint, error) {
	return sshx.LocalEndpoint(v), nil
}
