package backend

import (
	"fmt"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/config"
)

// apkovlBackend is Alpine's provisioning mechanism: a *.apkovl.tar.gz served
// to the guest by QEMU's vvfat. It applies to a genuine live boot, and to
// the installer environment of an uninstalled disk-mode VM. setup-disk in
// sys mode copies the running system, including stoat's key, onto the
// target. Without the overlay the installed guest is unreachable.
type apkovlBackend struct{}

func (apkovlBackend) Name() string { return "apkovl" }

// applies reports whether v is in one of the two states that need an
// overlay: a genuine live boot, or a disk-mode install still running its
// installer. An installed disk VM and a cloud VM get nothing.
func applies(v *config.VM) bool {
	return v.Mode == "live" || (v.Mode == "disk" && !v.Installed)
}

// Prepare rebuilds the overlay on every start where applies is true.
// Unlike the cloudinit backend, there is no staleness check: a live or
// installer VM is disposable. Always rebuilding is simpler than tracking
// whether the previous build is still good, and costs nothing a live boot
// doesn't already pay for. See docs/design/guest-subsystem.md §10 ("Risks")
// for why this asymmetry with cloudinit's once-ever Prepare is intentional.
func (apkovlBackend) Prepare(v *config.VM) error {
	if !applies(v) {
		return nil
	}
	if err := apkovl.Build(v); err != nil {
		return fmt.Errorf("building apkovl: %w", err)
	}
	if err := apkovl.ExtractKernel(v); err != nil {
		return fmt.Errorf("extracting boot kernel: %w", err)
	}
	return nil
}

// Args boots the kernel and initramfs directly (Prepare extracted them from the
// ISO) and attaches the vvfat overlay drive. -kernel bypasses the ISO's
// ISOLINUX, whose boot: prompt hangs under QEMU; the ISO stays attached in
// qemu.Args's Mode switch so the initramfs still finds modloop on it. QEMU
// boots -kernel regardless of the -boot order there.
func (apkovlBackend) Args(v *config.VM) []string {
	if !applies(v) {
		return nil
	}
	args := []string{
		"-kernel", apkovl.KernelPath(v),
		"-initrd", apkovl.InitramfsPath(v),
		"-append", apkovl.KernelAppend,
		"-drive", "file=fat:rw:" + v.OvlDir() + ",format=raw,if=virtio",
	}
	// The disk installer still boots this kernel, so a guest reboot would
	// re-enter setup-alpine and wipe the disk again. -no-reboot turns the
	// install script's poweroff, or any reboot, into a QEMU exit. Start then
	// marks the VM installed and boots the disk instead. A live VM has no disk
	// to protect, so its reboot stays a reboot.
	if v.Mode == "disk" && !v.Installed {
		args = append(args, "-no-reboot")
	}
	return args
}
