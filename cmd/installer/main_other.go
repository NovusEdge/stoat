//go:build !linux

package main

import (
	"fmt"
	"os"
	"runtime"
)

// This build does not import internal/installer at all, because that
// package imports kvm_linux.go's KVMCheck unconditionally, so it does not
// compile on this GOOS in the first place. That is the whole reason this
// message needs its own file: it is the actual thing a non-Linux user sees,
// in place of a bare "undefined: KVMCheck" from the compiler.
func run() int {
	fmt.Fprintln(os.Stderr, "stoat is Linux-only: it needs KVM, and it does not compile for "+runtime.GOOS+" yet.")
	return 1
}
