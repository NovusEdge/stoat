package sshx

import "github.com/novusedge/stoat/internal/config"

// Endpoint is where ssh(1) and scp(1) reach a guest, and under what host key
// policy. A QEMU VM answers on a loopback forward, where an unchecked host key
// costs nothing. A cloud instance answers on a routable address, where the
// same setting is a machine-in-the-middle hole, so that endpoint names a
// per-VM known_hosts file instead.
type Endpoint struct {
	Name string
	Host string
	Port int
	User string

	// KnownHosts is the known_hosts file to pin against. Empty keeps the
	// loopback policy: no host key checking at all.
	KnownHosts string
}

// LocalEndpoint is the endpoint for a VM reached through QEMU's user-mode
// port forward.
func LocalEndpoint(v *config.VM) Endpoint {
	return Endpoint{
		Name: v.Name,
		Host: "127.0.0.1",
		Port: v.SSHPort,
		User: User(v),
	}
}
