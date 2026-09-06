package qemu

import (
	"context"
	"fmt"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

const (
	CPUModelHost       = "host"
	RequiredCPUX8664V2 = "x86-64-v2"
)

var x8664v2Features = []string{
	"cx16",
	"lahf-lm",
	"popcnt",
	"pni",
	"ssse3",
	"sse4.1",
	"sse4.2",
}

// expandCPUModel is a seam for the contract test. Production uses a separate
// QEMU/KVM probe with the same CPU model as the VM launch.
var expandCPUModel = queryCPUModelExpansion

// validateCPU checks a selected image's launch contract before Start prepares
// the monitor, boot media, backend, or shares. Running may remove a stale PID
// file before this point; empty fields retain the legacy implicit QEMU CPU.
func validateCPU(v *config.VM) error {
	if v == nil {
		return fmt.Errorf("%w: nil VM CPU contract", ErrStartFailed)
	}
	if v.CPUModel == "" && v.RequiredCPU == "" {
		return nil
	}
	if v.CPUModel != CPUModelHost || v.RequiredCPU != RequiredCPUX8664V2 {
		return fmt.Errorf("%w: unsupported CPU contract cpu_model=%q required_cpu=%q", ErrStartFailed, v.CPUModel, v.RequiredCPU)
	}

	props, err := expandCPUModel(context.Background(), v.CPUModel)
	if err != nil {
		return fmt.Errorf("%w: CPU probe failed: %w", ErrStartFailed, err)
	}
	missing, err := missingCPUFeatures(v.RequiredCPU, props)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStartFailed, err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: CPU contract %s is unavailable; missing features: %s", ErrStartFailed, v.RequiredCPU, strings.Join(missing, ", "))
	}
	return nil
}

func missingCPUFeatures(required string, props map[string]bool) ([]string, error) {
	var requiredFeatures []string
	switch required {
	case RequiredCPUX8664V2:
		requiredFeatures = x8664v2Features
	default:
		return nil, fmt.Errorf("unsupported required CPU baseline %q", required)
	}

	missing := make([]string, 0)
	for _, feature := range requiredFeatures {
		if !props[feature] {
			missing = append(missing, feature)
		}
	}
	return missing, nil
}

func cpuProbeArgs(model string) []string {
	return []string{
		"-machine", "pc,accel=kvm",
		"-cpu", model,
		"-smp", "1",
		"-nodefaults",
		"-display", "none",
		"-S",
		"-qmp", "stdio",
	}
}
