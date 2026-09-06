//go:build linux

package hostops

// RequireVM permits native VM operations on Linux, the currently qualified
// host platform.
func RequireVM() error { return nil }
