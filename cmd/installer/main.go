// Command installer builds stoat from this clone and installs it.
//
// Run it with `just setup`. It is deliberately not a released artifact: it
// builds from source, so it only makes sense from a checkout.
package main

import "os"

// main dispatches to run, defined per platform: main_linux.go builds and
// installs stoat, main_other.go prints the Linux-only message.
//
// A runtime.GOOS check here cannot replace this split. internal/installer
// imports kvm_linux.go's KVMCheck unconditionally, so this package refuses
// to compile on a non-Linux GOOS. Code that never compiles never runs its
// check. See kvm_linux.go's comment for the same platform-seam pattern.
func main() {
	os.Exit(run())
}
