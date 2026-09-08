package qemu

import (
	"context"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	"github.com/novusedge/stoat/internal/testutil"
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

func TestStatusReflectsTheQemuProcess(t *testing.T) {
	v := &config.VM{Name: "dev", Dir: t.TempDir()}

	s, err := Provider{}.Status(context.Background(), v)
	if err != nil || s.Running || !s.StartedAt.IsZero() {
		t.Fatalf("Status() = %+v, %v, want a stopped zero status", s, err)
	}

	defer testutil.FakeRunning(t, v.Dir)()
	s, err = Provider{}.Status(context.Background(), v)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !s.Running {
		t.Error("Running = false, want true while the pidfile names a live process")
	}
	if s.StartedAt.IsZero() {
		t.Error("StartedAt is zero, want the pidfile's mtime")
	}
}
