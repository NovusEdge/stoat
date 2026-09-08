package sshx

import (
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestLocalEndpoint(t *testing.T) {
	v := &config.VM{Name: "dev", SSHPort: 2222, SSHUser: "stoat"}
	e := LocalEndpoint(v)
	if e.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", e.Host)
	}
	if e.Port != 2222 {
		t.Errorf("Port = %d, want 2222", e.Port)
	}
	if e.User != "stoat" {
		t.Errorf("User = %q, want stoat", e.User)
	}
	if e.Name != "dev" {
		t.Errorf("Name = %q, want dev", e.Name)
	}
	if e.KnownHosts != "" {
		t.Errorf("KnownHosts = %q, want empty for a loopback forward", e.KnownHosts)
	}
}

func TestLocalEndpointDefaultsUserToRoot(t *testing.T) {
	e := LocalEndpoint(&config.VM{Name: "live", SSHPort: 2200})
	if e.User != "root" {
		t.Errorf("User = %q, want root", e.User)
	}
}
