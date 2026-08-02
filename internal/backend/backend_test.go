package backend

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func joined(a []string) string { return strings.Join(a, " ") }

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
