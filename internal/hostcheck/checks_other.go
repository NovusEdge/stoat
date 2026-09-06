//go:build !linux

package hostcheck

import "github.com/novusedge/stoat/internal/hostops"

// RunChecks reports the native qualification boundary without probing Linux
// binaries or /dev/kvm. Those requirements do not describe this host.
func RunChecks(_ Distro) []Check {
	err := hostops.RequireVM()
	return []Check{{
		Name:   "native VM operations",
		Detail: err.Error(),
	}}
}
