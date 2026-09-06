// Command installer builds stoat from this clone and installs it.
//
// Run it with `just setup`. It is deliberately not a released artifact: it
// builds from source, so it only makes sense from a checkout.
package main

import "os"

// main dispatches to run, defined per platform: main_linux.go builds and
// installs stoat, while main_other.go reports that source installation stays
// Linux-only.
func main() {
	os.Exit(run())
}
