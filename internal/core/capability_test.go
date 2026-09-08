package core

import (
	"errors"
	"testing"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider/fake"
)

func TestRequireCapabilityAllowsASupportedOperation(t *testing.T) {
	f := fake.Install(t)
	f.Caps = []capabilities.Capability{{Name: "vm.snapshot", Status: capabilities.StatusSupported}}
	if err := RequireCapability(&config.VM{Name: "dev"}, "vm.snapshot"); err != nil {
		t.Errorf("RequireCapability() = %v, want nil", err)
	}
}

func TestRequireCapabilityRefusesAnUnsupportedOperation(t *testing.T) {
	f := fake.Install(t)
	f.Caps = []capabilities.Capability{{
		Name:   "vm.snapshot",
		Status: capabilities.StatusUnsupported,
		Reason: &capabilities.Reason{Code: capabilities.ReasonProviderUnsupported},
	}}
	err := RequireCapability(&config.VM{Name: "dev"}, "vm.snapshot")
	if err == nil {
		t.Fatal("RequireCapability() = nil, want a refusal")
	}
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Errorf("RequireCapability() = %v, want ErrCapabilityUnavailable", err)
	}
}

func TestRequireCapabilityRefusesAnUndeclaredCapability(t *testing.T) {
	fake.Install(t)
	if err := RequireCapability(&config.VM{Name: "dev"}, "vm.snapshot"); err == nil {
		t.Error("RequireCapability() = nil for a capability the provider never declared; want a refusal")
	}
}
