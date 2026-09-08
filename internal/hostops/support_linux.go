//go:build linux

package hostops

// RequireLocalHypervisor permits starting a VM on this host. Linux is the
// qualified platform.
func RequireLocalHypervisor() error { return nil }

// RequireDataRoot permits owning a data root. Reading, writing and listing
// VM records needs no hypervisor, so a host that cannot start a local VM can
// still manage one that runs elsewhere.
func RequireDataRoot() error { return nil }
