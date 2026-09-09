package provider

import (
	"context"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

// internal/sshx builds ssh argv for guests that no longer all answer on a
// loopback forward, and it cannot ask the registry itself: this package
// imports sshx, so the reverse would close a cycle. Installing the resolver
// here inverts that.
//
// Without this, sshx.Run and sshx.Provision dial 127.0.0.1 for every VM, and
// a cloud guest refuses the connection while every other command reports it
// running and reachable.
func init() {
	sshx.SetEndpointResolver(func(v *config.VM) (sshx.Endpoint, error) {
		p, err := For(v)
		if err != nil {
			return sshx.Endpoint{}, err
		}
		return p.Endpoint(context.Background(), v)
	})
}
