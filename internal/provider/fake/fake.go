// Package fake is a provider.Provider for tests. It lets a core test run with
// no QEMU process and no cloud API.
package fake

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	_ "github.com/novusedge/stoat/internal/provider/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

// Provider answers from a liveness map the test controls. core.waitStopped
// polls Status from its own goroutine while the test flips a VM, so every
// read and write of that map takes mu.
type Provider struct {
	StartErr   error
	StopErr    error
	CreateErr  error
	DestroyErr error
	Ep         sshx.Endpoint
	Caps       []capabilities.Capability

	mu      sync.Mutex
	running map[string]bool
}

func (*Provider) Name() string { return "qemu" }

// Capabilities answers like the QEMU provider this fake stands in for: every
// gated operation supported. A test that wants a refusal sets Caps itself.
//
// Returning nil would refuse everything, since RequireCapability treats an
// undeclared operation as unsupported. That would make every core test that
// clones, forwards or snapshots fail for a reason the test is not about.
func (p *Provider) Capabilities(*config.VM) []capabilities.Capability {
	if p.Caps != nil {
		return p.Caps
	}
	ops := []string{
		capabilities.OpSnapshot, capabilities.OpClone, capabilities.OpScreenshot,
		capabilities.OpSendKey, capabilities.OpForward, capabilities.OpConsoleLog,
		capabilities.OpShare, capabilities.OpUpdateRAM, capabilities.OpUpdateCPU,
	}
	out := make([]capabilities.Capability, len(ops))
	for i, name := range ops {
		out[i] = capabilities.Capability{Name: name, Status: capabilities.StatusSupported}
	}
	return out
}

// SetRunning marks name live. Tests that need a VM to look started without
// calling Start use this.
func (p *Provider) SetRunning(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running[name] = true
}

// SetStopped marks name dead.
func (p *Provider) SetStopped(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.running, name)
}

// IsRunning reports the recorded liveness of name.
func (p *Provider) IsRunning(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running[name]
}

func (p *Provider) Start(_ context.Context, v *config.VM) error {
	if p.StartErr != nil {
		return p.StartErr
	}
	p.SetRunning(v.Name)
	return nil
}

func (p *Provider) Stop(_ context.Context, v *config.VM) error {
	if p.StopErr != nil {
		return p.StopErr
	}
	p.SetStopped(v.Name)
	return nil
}

func (p *Provider) Status(_ context.Context, v *config.VM) (provider.Status, error) {
	if !p.IsRunning(v.Name) {
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

func (p *Provider) Create(_ context.Context, v *config.VM) error {
	if p.CreateErr != nil {
		return p.CreateErr
	}
	return nil
}

func (p *Provider) Destroy(_ context.Context, v *config.VM) error {
	if p.DestroyErr != nil {
		return p.DestroyErr
	}
	p.SetStopped(v.Name)
	return nil
}

// Install registers a fresh fake under "qemu" and restores the previous
// provider when the test ends.
func Install(t *testing.T) *Provider {
	t.Helper()
	prev, err := provider.For(&config.VM{})
	if err != nil {
		t.Fatalf("no provider registered as qemu: %v", err)
	}
	f := &Provider{running: map[string]bool{}}
	provider.Register("qemu", f)
	t.Cleanup(func() { provider.Register("qemu", prev) })
	return f
}
