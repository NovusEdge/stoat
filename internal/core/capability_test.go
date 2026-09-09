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

// Every gated operation must refuse on a provider that declares it
// unsupported, and succeed past the gate on one that does not. The first
// wiring of this gated on names neither provider used, so every check
// refused on QEMU too and no test noticed.
func TestGatedOperationsRefuseOnAnUnsupportingProvider(t *testing.T) {
	root(t)
	f := fake.Install(t)
	f.Caps = []capabilities.Capability{{
		Name:   capabilities.OpSnapshot,
		Status: capabilities.StatusUnsupported,
		Reason: &capabilities.Reason{Code: capabilities.ReasonProviderUnsupported},
	}}
	v := &config.VM{Name: "cloudy", Mode: "cloud", RAM: 1024, CPUs: 1, Disk: "10G", SSHPort: 2299}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := TakeSnapshot("cloudy", "tag"); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Errorf("TakeSnapshot = %v, want ErrCapabilityUnavailable", err)
	}
	if _, err := Snapshots("cloudy"); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Errorf("Snapshots = %v, want ErrCapabilityUnavailable", err)
	}
}

// The QEMU provider must declare every gated operation. A missing entry
// refuses that operation on a local VM, which is a regression no gce test
// would catch.
func TestQemuDeclaresEveryGatedOperation(t *testing.T) {
	root(t)
	v := &config.VM{Name: "local", Mode: "cloud", RAM: 1024, CPUs: 1, Disk: "10G", SSHPort: 2298}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	ops := []string{
		capabilities.OpSnapshot, capabilities.OpClone, capabilities.OpScreenshot,
		capabilities.OpSendKey, capabilities.OpForward, capabilities.OpConsoleLog,
		capabilities.OpShare, capabilities.OpUpdateRAM, capabilities.OpUpdateCPU,
	}
	for _, op := range ops {
		if err := RequireCapability(v, op); err != nil {
			t.Errorf("RequireCapability(%q) = %v, want nil on qemu", op, err)
		}
	}
}

// The reason must be readable as a field. A JSON or MCP caller branching on
// it should never have to parse the message, which would make the wording a
// contract.
func TestCapabilityErrorCarriesItsReasonInAField(t *testing.T) {
	root(t)
	f := fake.Install(t)
	f.Caps = []capabilities.Capability{{
		Name:   capabilities.OpSnapshot,
		Status: capabilities.StatusUnsupported,
		Reason: &capabilities.Reason{Code: capabilities.ReasonProviderUnsupported},
	}}
	v := &config.VM{Name: "cloudy", Mode: "cloud", RAM: 1024, CPUs: 1, Disk: "10G", SSHPort: 2297}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	err := RequireCapability(v, capabilities.OpSnapshot)
	var ce *CapabilityError
	if !errors.As(err, &ce) {
		t.Fatalf("RequireCapability() = %v, want a *CapabilityError", err)
	}
	if ce.Reason != capabilities.ReasonProviderUnsupported {
		t.Errorf("Reason = %q, want %q", ce.Reason, capabilities.ReasonProviderUnsupported)
	}
	if ce.Operation != capabilities.OpSnapshot || ce.VM != "cloudy" {
		t.Errorf("CapabilityError = %+v, want the vm and operation named", ce)
	}
	if !errors.Is(err, ErrCapabilityUnavailable) {
		t.Error("errors.Is(err, ErrCapabilityUnavailable) = false; existing callers match on the sentinel")
	}
}
