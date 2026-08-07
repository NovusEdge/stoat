//go:build !linux

package main

import (
	"fmt"
	"os"
	"runtime"
)

// This build skips internal/installer entirely. That package imports
// kvm_linux.go's KVMCheck unconditionally, so it refuses to compile on this
// GOOS. Without this file, a non-Linux user would see a bare
// "undefined: KVMCheck" compiler error instead of this message.
func run() int {
	fmt.Fprintln(os.Stderr, "stoat is Linux-only: it needs KVM, and it does not compile for "+runtime.GOOS+" yet.")
	return 1
}
