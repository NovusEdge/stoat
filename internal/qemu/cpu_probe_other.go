//go:build !linux

package qemu

import (
	"context"
	"fmt"
	"runtime"
)

// queryCPUModelExpansion is unavailable outside Linux because this wave's
// selected CPU contract depends on QEMU's KVM accelerator.
func queryCPUModelExpansion(context.Context, string) (map[string]bool, error) {
	return nil, fmt.Errorf("qemu/kvm CPU probe unsupported on %s", runtime.GOOS)
}
