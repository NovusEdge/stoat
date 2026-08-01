// Command installer builds stoat from this clone and installs it.
//
// Run it with `just setup`. It is deliberately not a released artifact: it
// builds from source, so it only makes sense from a checkout.
package main

import "os"

// main just dispatches to run, which is defined per platform:
// main_linux.go builds and installs stoat, main_other.go prints the friendly
// "Linux-only" message. Splitting it this way, rather than gating the
// message with a runtime.GOOS check here, is what makes the message
// reachable at all -- internal/installer imports kvm_linux.go's KVMCheck
// unconditionally, so this package does not compile on a non-Linux GOOS in
// the first place, and an import that never compiles can never run its
// runtime check. See kvm_linux.go's comment: this is the same platform-seam
// pattern stoat already uses.
func main() {
	os.Exit(run())
}
