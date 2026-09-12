//go:build !linux

package hostcheck

// AvailableMB reports nothing on a host that runs no local VM. Callers treat
// 0 as "the host did not say" and skip the check.
func AvailableMB() int { return 0 }
