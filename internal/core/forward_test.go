package core

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestForwardSavesAndReadsBack(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	want := []PortForward{{HostPort: 8080, GuestPort: 80}}
	if _, err := Forward("web", want); err != nil {
		t.Fatal(err)
	}

	got, err := Get("web")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Forwards, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// Persisted, not just in-memory: reload from disk and check again.
	reloaded, err := config.Load("web")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Forwards, want) {
		t.Errorf("not persisted: got %+v, want %+v", reloaded.Forwards, want)
	}
}

func TestForwardOnRunningVMSavesButSignalsPending(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	stop := fakeRunning(t, v)
	defer stop()

	active, err := Forward("web", []PortForward{{HostPort: 8080, GuestPort: 80}})
	if err != nil {
		t.Fatalf("Forward on a running VM must SUCCEED, not error: %v", err)
	}
	if active {
		t.Error("active = true on a running VM; qemu cannot hot-add a hostfwd to a live netdev")
	}

	// The save still happened. "Takes effect at next start" is not a refusal,
	// and the two must never look the same to a caller, which is exactly why
	// this is a return value and not a sentinel error: a caller writing the
	// ordinary `if err != nil` would otherwise report a failure for an
	// operation that wrote vm.toml.
	got, err := Get("web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Forwards) != 1 || got.Forwards[0].HostPort != 8080 {
		t.Errorf("forward was not saved despite the VM running: %+v", got.Forwards)
	}
}

// A stopped VM's forwards are live the moment the VM next starts, so Forward
// reports them as active immediately: there is nothing pending.
func TestForwardOnStoppedVMIsActive(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	active, err := Forward("web", []PortForward{{HostPort: 8080, GuestPort: 80}})
	if err != nil || !active {
		t.Fatalf("Forward = %v, %v; want active, no error", active, err)
	}
}

func TestForwardRejectsOutOfRangePorts(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		fwds []PortForward
	}{
		{"host port zero", []PortForward{{HostPort: 0, GuestPort: 80}}},
		{"host port negative", []PortForward{{HostPort: -1, GuestPort: 80}}},
		{"host port too large", []PortForward{{HostPort: 65536, GuestPort: 80}}},
		{"guest port zero", []PortForward{{HostPort: 8080, GuestPort: 0}}},
		{"guest port negative", []PortForward{{HostPort: 8080, GuestPort: -1}}},
		{"guest port too large", []PortForward{{HostPort: 8080, GuestPort: 65536}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Forward("web", tc.fwds); !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("want ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestForwardRejectsPrivilegedHostPort(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Forward("web", []PortForward{{HostPort: 80, GuestPort: 80}})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a privileged host port, got %v", err)
	}
}

func TestForwardRejectsDuplicateHostPortsInOneRequest(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Forward("web", []PortForward{
		{HostPort: 8080, GuestPort: 80},
		{HostPort: 8080, GuestPort: 443},
	})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a duplicate host port, got %v", err)
	}
}

func TestForwardRejectsCollisionWithOwnSSHPort(t *testing.T) {
	root(t)
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Forward("web", []PortForward{{HostPort: 2200, GuestPort: 80}})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a forward colliding with this VM's own ssh port, got %v", err)
	}
}

func TestForwardRejectsCollisionWithAnotherVMsSSHPort(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	_, err := Forward("web", []PortForward{{HostPort: 2201, GuestPort: 80}})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a forward colliding with another VM's ssh port, got %v", err)
	}
	if !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("error does not name the colliding VM: %v", err)
	}
	if !strings.Contains(err.Error(), "ssh port") {
		t.Errorf("error does not say the collision is with an ssh port: %v", err)
	}
}

func TestForwardRejectsCollisionWithAnotherVMsForward(t *testing.T) {
	root(t)
	other := &config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Forward("other", []PortForward{{HostPort: 8080, GuestPort: 80}}); err != nil {
		t.Fatal(err)
	}

	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Forward("web", []PortForward{{HostPort: 8080, GuestPort: 443}})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a forward colliding with another VM's forward, got %v", err)
	}
	if !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("error does not name the colliding VM: %v", err)
	}
	if !strings.Contains(err.Error(), "declared forward") {
		t.Errorf("error does not say the collision is with a declared forward: %v", err)
	}
}

// A broken VM (vm.toml exists but fails to parse) still claims its ssh port
// via config.BrokenSSHPort, and the refusal must still name it: an agent or
// user asking "which VM" cannot be told to go inspect a VM that config.List
// cannot even enumerate as healthy.
func TestForwardRejectsCollisionWithBrokenVMsSSHPort(t *testing.T) {
	dir := root(t)
	writeBroken(t, dir, "hosed", "sshport = 2201")

	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	_, err := Forward("web", []PortForward{{HostPort: 2201, GuestPort: 80}})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("want ErrInvalidSpec for a forward colliding with a broken VM's ssh port, got %v", err)
	}
	if !strings.Contains(err.Error(), `"hosed"`) {
		t.Errorf("error does not name the colliding broken VM: %v", err)
	}
	if !strings.Contains(err.Error(), "ssh port") {
		t.Errorf("error does not say the collision is with an ssh port: %v", err)
	}
}

func TestForwardAllowsDistinctPortsAcrossVMs(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Forward("web", []PortForward{{HostPort: 8080, GuestPort: 80}}); err != nil {
		t.Fatalf("distinct ports across VMs should be allowed: %v", err)
	}
}

func TestForwardRejectsUnknownVM(t *testing.T) {
	root(t)
	if _, err := Forward("nope", []PortForward{{HostPort: 8080, GuestPort: 80}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
