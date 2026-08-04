package backend

import (
	"fmt"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/config"
)

// apkovlBackend is Alpine's provisioning mechanism: a *.apkovl.tar.gz served
// to the guest by QEMU's vvfat, applied both to a genuine live boot and to
// the installer environment of an uninstalled disk-mode VM (setup-disk in
// sys mode copies the running system, including stoat's key, onto the
// target; without the overlay the installed guest is unreachable).
type apkovlBackend struct{}

func (apkovlBackend) Name() string { return "apkovl" }

// applies reports whether v is in one of the two states that need an
// overlay: a genuine live boot, or a disk-mode install still running its
// installer. An installed disk VM and a cloud VM get nothing.
func applies(v *config.VM) bool {
	return v.Mode == "live" || (v.Mode == "disk" && !v.Installed)
}

// Prepare rebuilds the overlay on every start applies is true for. Unlike
// the cloudinit backend, there is no staleness check: a live/installer VM is
// disposable, so always rebuilding is simpler than tracking whether the
// previous build is still good, and it costs nothing a live boot doesn't
// already pay for. See docs/design/guest-subsystem.md §10 ("Risks") for why
// this asymmetry with cloudinit's once-ever Prepare is intentional.
func (apkovlBackend) Prepare(v *config.VM) error {
	if !applies(v) {
		return nil
	}
	if err := apkovl.Build(v); err != nil {
		return fmt.Errorf("building apkovl: %w", err)
	}
	return nil
}

// Args attaches the vvfat overlay drive whenever applies is true. The
// -boot d that makes QEMU prefer the ISO over this drive's empty MBR stays
// in qemu.Args's Mode switch, alongside the rest of that mode's boot media.
func (apkovlBackend) Args(v *config.VM) []string {
	if !applies(v) {
		return nil
	}
	return []string{"-drive", "file=fat:rw:" + v.OvlDir() + ",format=raw,if=virtio"}
}
