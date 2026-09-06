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
	fmt.Fprintln(os.Stderr, "stoat command is buildable for diagnostics on "+runtime.GOOS+"/"+runtime.GOARCH+", but native VM operations are not qualified there; source installation remains Linux-only.")
	return 1
}
