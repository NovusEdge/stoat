package qemu

import (
	"context"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
)

func TestRegisteredAsQemu(t *testing.T) {
	p, err := provider.For(&config.VM{Name: "dev"})
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if p.Name() != "qemu" {
		t.Errorf("Name() = %q, want qemu", p.Name())
	}
}

func TestEndpointIsLoopback(t *testing.T) {
	p := Provider{}
	e, err := p.Endpoint(context.Background(), &config.VM{Name: "dev", SSHPort: 2222})
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if e.Host != "127.0.0.1" || e.Port != 2222 {
		t.Errorf("Endpoint() = %s:%d, want 127.0.0.1:2222", e.Host, e.Port)
	}
	if e.KnownHosts != "" {
		t.Errorf("KnownHosts = %q, want empty: a loopback forward is not pinned", e.KnownHosts)
	}
}

func TestStatusReportsStoppedForAnUnstartedVM(t *testing.T) {
	s, err := Provider{}.Status(context.Background(), &config.VM{Name: "dev", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if s.Running {
		t.Error("Running = true, want false for a VM with no pidfile")
	}
	if s.Raw != "" {
		t.Errorf("Raw = %q, want empty: qemu has no status word of its own", s.Raw)
	}
}
