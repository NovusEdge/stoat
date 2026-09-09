// Package qemu implements provider.Provider over internal/qemu, the local
// hypervisor. It holds no logic of its own: every call forwards.
package qemu

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

func init() { provider.Register("qemu", Provider{}) }

type Provider struct{}

func (Provider) Name() string { return "qemu" }

// qemuCapabilities is what a local QEMU VM supports, which is everything.
// The gated operation names come first, so this list reads against
// gce.unsupported; the rest describe the target to `stoat capabilities`.
//
// Every Op* constant must appear here. RequireCapability treats an
// undeclared name as a refusal, so omitting one refuses that operation on
// QEMU, which is how the first wiring of this broke snapshots.
var qemuCapabilities = []string{
	capabilities.OpSnapshot, capabilities.OpClone, capabilities.OpScreenshot,
	capabilities.OpSendKey, capabilities.OpForward, capabilities.OpConsoleLog,
	capabilities.OpShare, capabilities.OpUpdateRAM, capabilities.OpUpdateCPU,

	"vm.lifecycle", "vm.snapshot", "recipes",
	"mcp.guest.observe", "mcp.guest.manage", "mcp.guest.exec", "cli.guest.shell",
}

func (Provider) Capabilities(*config.VM) []capabilities.Capability {
	out := make([]capabilities.Capability, len(qemuCapabilities))
	for i, name := range qemuCapabilities {
		out[i] = capabilities.Capability{Name: name, Status: capabilities.StatusSupported}
	}
	return out
}

func (Provider) Start(_ context.Context, v *config.VM) error { return qemu.Start(v) }

func (Provider) Stop(_ context.Context, v *config.VM) error { return qemu.Stop(v) }

func (Provider) Status(_ context.Context, v *config.VM) (provider.Status, error) {
	return provider.Status{Running: qemu.Running(v), StartedAt: qemu.StartedAt(v)}, nil
}

func (Provider) Endpoint(_ context.Context, v *config.VM) (sshx.Endpoint, error) {
	return sshx.LocalEndpoint(v), nil
}

// Create allocates the qcow2 disk for a "disk" mode VM. Live mode boots the
// ISO into a tmpfs root and has no disk to allocate.
func (Provider) Create(_ context.Context, v *config.VM) error {
	if v.Mode != "disk" {
		return nil
	}
	out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), v.Disk).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Destroy stops the process. The disk itself lives in v.Dir, which
// config.VM.Delete removes.
func (Provider) Destroy(_ context.Context, v *config.VM) error {
	return qemu.Stop(v)
}
