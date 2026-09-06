//go:build !linux

package main

import (
	"fmt"
	"os"
	"runtime"
)

// The source installer remains Linux-only. The command binary itself builds
// on other hosts so users can run read-only diagnostics, while native VM
// operations remain unqualified there.
func run() int {
	host := runtime.GOOS + "/" + runtime.GOARCH
	fmt.Fprintln(os.Stderr, "The guided installer runs on Linux only. This host is "+host+".")
	fmt.Fprintln(os.Stderr, "Run `just install` to build the diagnostic binary here.")
	fmt.Fprintln(os.Stderr, "Native VM operations are not qualified on "+host+".")
	return 1
}
