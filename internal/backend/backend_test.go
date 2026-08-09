package backend

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/config"
)

func joined(a []string) string { return strings.Join(a, " ") }

// TestApkovlBootsKernelDirectly proves a live or uninstalled-disk Alpine VM
// boots -kernel/-initrd, not the ISO's ISOLINUX, whose boot: prompt hangs under
// QEMU. Prepare extracts the two files; Args points QEMU at them.
func TestApkovlBootsKernelDirectly(t *testing.T) {
	v := &config.VM{
		Name: "live1", Mode: "live", OS: "alpine", Backend: "apkovl",
		Dir: filepath.Join(t.TempDir(), "live1"),
	}
	got := joined(For(v).Args(v))
	for _, want := range []string{
		"-kernel " + apkovl.KernelPath(v),
		"-initrd " + apkovl.InitramfsPath(v),
		"-append " + apkovl.KernelAppend,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("apkovl Args missing %q, so it would boot via the hanging ISOLINUX:\n%s", want, got)
		}
	}
}

// TestDiskInstallerExitsOnReboot pins -no-reboot to the uninstalled disk boot.
// The installer kernel is still attached, so a guest reboot would re-run
// setup-alpine and wipe the disk again; -no-reboot makes that reboot exit QEMU,
// and Start marks the VM installed at the next boot. A live VM keeps its reboot.
func TestDiskInstallerExitsOnReboot(t *testing.T) {
	disk := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl", Installed: false,
		Dir: filepath.Join(t.TempDir(), "work"),
	}
	if got := joined(For(disk).Args(disk)); !strings.Contains(got, "-no-reboot") {
		t.Errorf("disk installer missing -no-reboot, so a guest reboot would reinstall:\n%s", got)
	}
	live := &config.VM{
		Name: "live1", Mode: "live", OS: "alpine", Backend: "apkovl",
		Dir: filepath.Join(t.TempDir(), "live1"),
	}
	if got := joined(For(live).Args(live)); strings.Contains(got, "-no-reboot") {
		t.Errorf("live VM got -no-reboot; its reboot must stay a reboot:\n%s", got)
	}
}

func TestAlpineLiveGetsTheOverlayDrive(t *testing.T) {
	v := &config.VM{
		Name: "live1", Mode: "live", OS: "alpine", Backend: "apkovl",
		Dir: filepath.Join(t.TempDir(), "live1"),
	}
	b := For(v)
	if b.Name() != "apkovl" {
		t.Fatalf("Name() = %q, want apkovl", b.Name())
	}
	got := joined(b.Args(v))
	want := "-drive file=fat:rw:" + v.OvlDir() + ",format=raw,if=virtio"
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in %q", want, got)
	}
}

func TestAlpineDiskInstalledGetsNoOverlayDrive(t *testing.T) {
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl", Installed: true,
		Dir: filepath.Join(t.TempDir(), "work"),
	}
	got := joined(For(v).Args(v))
	if strings.Contains(got, "fat:rw:") {
		t.Errorf("installed disk VM got an overlay drive: %q", got)
	}
}

func TestUbuntuCloudGetsTheSeedCdrom(t *testing.T) {
	v := &config.VM{
		Name: "cloudy", Mode: "cloud", OS: "ubuntu", Backend: "cloudinit",
		Dir: filepath.Join(t.TempDir(), "cloudy"),
	}
	b := For(v)
	if b.Name() != "cloudinit" {
		t.Fatalf("Name() = %q, want cloudinit", b.Name())
	}
	got := joined(b.Args(v))
	want := "-drive file=" + filepath.Join(v.OvlDir(), "seed.iso") + ",media=cdrom"
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in %q", want, got)
	}
}

// TestUbuntuDiskGetsNoSeedCdrom pins the mode guard described on
// cloudinitBackend: a BYO Ubuntu ISO installed in disk mode still has
// Backend "cloudinit" from the registry (it's still Ubuntu), but it has no
// seed and must not be given one.
func TestUbuntuDiskGetsNoSeedCdrom(t *testing.T) {
	v := &config.VM{
		Name: "byo-ubuntu", Mode: "disk", OS: "ubuntu", Backend: "cloudinit", Installed: false,
		Dir: filepath.Join(t.TempDir(), "byo-ubuntu"),
	}
	got := joined(For(v).Args(v))
	if strings.Contains(got, "media=cdrom") {
		t.Errorf("a BYO disk-mode install got a cloud-init seed cdrom: %q", got)
	}
	if err := For(v).Prepare(v); err != nil {
		t.Fatalf("Prepare returned an error for a disk-mode VM: %v", err)
	}
}

// The alpine-cloud catalog entry is OS "alpine" but Backend "cloudinit". It
// is the one image whose backend contradicts its OS's registry default, and
// the reason For keys off v.Backend rather than v.OS. Dispatching on OS
// would hand this VM an apkovl overlay it does not boot from, and no seed
// at all. The VM would come up unprovisioned with no clue why.
func TestAlpineCloudUsesItsEntrysBackendNotItsOSDefault(t *testing.T) {
	v := &config.VM{
		Name: "alpcloud", Mode: "cloud", OS: "alpine", Backend: "cloudinit",
		Dir: filepath.Join(t.TempDir(), "alpcloud"),
	}
	b := For(v)
	if b.Name() != "cloudinit" {
		t.Fatalf("Name() = %q, want cloudinit; v.Backend must win over the registry's apkovl default for alpine", b.Name())
	}
	got := strings.Join(b.Args(v), " ")
	if !strings.Contains(got, "seed.iso") {
		t.Errorf("no cloud-init seed attached: %q", got)
	}
	if strings.Contains(got, "fat:rw:") {
		t.Errorf("attached an apkovl overlay to a cloud VM: %q", got)
	}
}

// A VM whose Backend was never populated still has to resolve, or an older
// vm.toml stops working. Falling back to the registry is only for that case.
func TestEmptyBackendFallsBackToTheRegistry(t *testing.T) {
	v := &config.VM{
		Name: "old", Mode: "live", OS: "alpine",
		Dir: filepath.Join(t.TempDir(), "old"),
	}
	if got := For(v).Name(); got != "apkovl" {
		t.Errorf("Name() = %q, want apkovl from guest.Lookup", got)
	}
}

func TestUnknownOSGetsNothingAndDoesNotPanic(t *testing.T) {
	v := &config.VM{
		Name: "mystery", Mode: "live", OS: "plan9",
		Dir: filepath.Join(t.TempDir(), "mystery"),
	}
	b := For(v)
	if b.Name() != "ssh" {
		t.Fatalf("Name() = %q, want the no-op ssh backend", b.Name())
	}
	if args := b.Args(v); args != nil {
		t.Errorf("Args() = %v, want nil", args)
	}
	if err := b.Prepare(v); err != nil {
		t.Errorf("Prepare() = %v, want nil", err)
	}
}
