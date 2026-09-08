// Package fake is a provider.Provider for tests. It lets a core test run with
// no QEMU process and no cloud API.
package fake

import (
	"context"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	_ "github.com/novusedge/stoat/internal/provider/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

type Provider struct {
	RunningVMs map[string]bool
	StartErr   error
	StopErr    error
	Ep         sshx.Endpoint
}

func (Provider) Name() string                                      { return "qemu" }
func (Provider) Capabilities(*config.VM) []capabilities.Capability { return nil }

func (p *Provider) Start(_ context.Context, v *config.VM) error {
	if p.StartErr != nil {
		return p.StartErr
	}
	p.RunningVMs[v.Name] = true
	return nil
}

func (p *Provider) Stop(_ context.Context, v *config.VM) error {
	if p.StopErr != nil {
		return p.StopErr
	}
	delete(p.RunningVMs, v.Name)
	return nil
}

func (p *Provider) Status(_ context.Context, v *config.VM) (provider.Status, error) {
	if !p.RunningVMs[v.Name] {
		return provider.Status{}, nil
	}
	return provider.Status{Running: true, StartedAt: time.Unix(1, 0)}, nil
}

func (p *Provider) Endpoint(_ context.Context, v *config.VM) (sshx.Endpoint, error) {
	if p.Ep.Host != "" {
		return p.Ep, nil
	}
	return sshx.LocalEndpoint(v), nil
}

// Install registers a fresh fake under "qemu" and restores the previous
// provider when the test ends.
func Install(t *testing.T) *Provider {
	t.Helper()
	prev, err := provider.For(&config.VM{})
	if err != nil {
		t.Fatalf("no provider registered as qemu: %v", err)
	}
	f := &Provider{RunningVMs: map[string]bool{}}
	provider.Register("qemu", f)
	t.Cleanup(func() { provider.Register("qemu", prev) })
	return f
}
