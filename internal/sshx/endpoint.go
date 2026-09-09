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

// endpointResolver is how sshx reaches a VM that does not answer on a
// loopback forward. internal/provider installs it at init; sshx cannot ask
// the provider registry itself, since provider imports this package.
//
// A nil resolver means loopback, which is what every test and every
// pre-provider caller gets.
var endpointResolver func(*config.VM) (Endpoint, error)

// SetEndpointResolver installs the provider-backed resolver. Called once,
// from internal/provider's init.
func SetEndpointResolver(f func(*config.VM) (Endpoint, error)) { endpointResolver = f }

// endpointFor is where this package's own ssh invocations connect. Provision,
// RunCheck and Run all reach a guest that may not be local, so none of them
// may build a LocalEndpoint directly.
func endpointFor(v *config.VM) (Endpoint, error) {
	if endpointResolver == nil {
		return LocalEndpoint(v), nil
	}
	return endpointResolver(v)
}

// mustEndpoint is endpointFor for the call sites inside this package that
// build an ssh argv inline. A resolver failure means the VM's provider is
// unreachable or unknown; falling back to loopback there would dial a port
// on this host that belongs to nothing, so the zero Endpoint is returned
// instead and ssh fails naming an empty host.
func mustEndpoint(v *config.VM) Endpoint {
	e, err := endpointFor(v)
	if err != nil {
		return Endpoint{Name: v.Name, User: User(v)}
	}
	return e
}
