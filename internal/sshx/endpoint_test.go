package sshx

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func joined(a []string) string { return strings.Join(a, " ") }

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

func TestConnOptionsLoopbackKeepsUncheckedPolicy(t *testing.T) {
	got := joined(connOptions(Endpoint{Host: "127.0.0.1", Port: 2222}))
	if !strings.Contains(got, "StrictHostKeyChecking=no") {
		t.Errorf("loopback options = %q, want StrictHostKeyChecking=no", got)
	}
	if !strings.Contains(got, "UserKnownHostsFile=/dev/null") {
		t.Errorf("loopback options = %q, want UserKnownHostsFile=/dev/null", got)
	}
}

func TestConnOptionsPinsWhenKnownHostsSet(t *testing.T) {
	got := joined(connOptions(Endpoint{Host: "34.1.2.3", Port: 22, KnownHosts: "/tmp/kh"}))
	if !strings.Contains(got, "StrictHostKeyChecking=accept-new") {
		t.Errorf("pinned options = %q, want StrictHostKeyChecking=accept-new", got)
	}
	if !strings.Contains(got, "UserKnownHostsFile=/tmp/kh") {
		t.Errorf("pinned options = %q, want the named known_hosts file", got)
	}
	if strings.Contains(got, "/dev/null") {
		t.Errorf("pinned options = %q, must not discard the host key", got)
	}
}

func TestArgsUsesEndpointHostAndPort(t *testing.T) {
	got := joined(Args(Endpoint{Host: "34.1.2.3", Port: 22, User: "stoat"}, "uptime"))
	if !strings.Contains(got, "-p 22") {
		t.Errorf("args = %q, want -p 22", got)
	}
	if !strings.Contains(got, "stoat@34.1.2.3") {
		t.Errorf("args = %q, want stoat@34.1.2.3", got)
	}
	if !strings.HasSuffix(got, "uptime") {
		t.Errorf("args = %q, want the remote command last", got)
	}
}

func TestCopyArgsUsesEndpointHostAndPort(t *testing.T) {
	e := Endpoint{Host: "34.1.2.3", Port: 22, User: "stoat"}
	got := joined(CopyArgs(e, "/local", "/remote", true))
	if !strings.Contains(got, "-P 22") {
		t.Errorf("copy args = %q, want -P 22", got)
	}
	if !strings.HasSuffix(got, "/local stoat@34.1.2.3:/remote") {
		t.Errorf("copy args = %q, want local then remote", got)
	}
}
