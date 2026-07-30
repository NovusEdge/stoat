package qemu

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func joined(a []string) string { return strings.Join(a, " ") }

func TestArgsLive(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "live1", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 4, Share: "/home/u/vms", SSHPort: 2201,
		Dir: filepath.Join("/data", "live1"),
	}
	got := joined(Args(v))

	for _, want := range []string{
		"-enable-kvm", "-m 4096", "-smp 4",
		"-daemonize", "-pidfile /data/live1/qemu.pid",
		"-monitor unix:/data/live1/monitor.sock,server,nowait",
		"-netdev user,id=n0,hostfwd=tcp:127.0.0.1:2201-:22",
		"-device virtio-net,netdev=n0",
		"-virtfs local,path=/home/u/vms,mount_tag=host,security_model=none",
		"-cdrom /data/isos/alpine.iso",
		"-drive file=fat:rw:/data/live1/ovl,format=raw,if=virtio",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "disk.qcow2") {
		t.Error("live mode must not attach a disk")
	}
	if strings.Contains(got, "hostfwd=tcp::") {
		t.Error("hostfwd must bind 127.0.0.1, not all interfaces")
	}
	if !strings.Contains(got, "-boot d") {
		t.Errorf("live mode must set boot order so the ISO wins over the empty overlay:\n%s", got)
	}
}

func TestArgsDiskNotInstalled(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "work", Mode: "disk", ISO: "isos/alpine.iso",
		RAM: 8192, CPUs: 4, Disk: "8G", Installed: false,
		Share: "/home/u/vms", SSHPort: 2202,
		Dir: filepath.Join("/data", "work"),
	}
	got := joined(Args(v))

	if !strings.Contains(got, "-drive file=/data/work/disk.qcow2,if=virtio") {
		t.Errorf("disk not attached:\n%s", got)
	}
	if !strings.Contains(got, "-cdrom /data/isos/alpine.iso") || !strings.Contains(got, "-boot d") {
		t.Errorf("uninstalled disk VM must boot the ISO first:\n%s", got)
	}
	if strings.Contains(got, "fat:rw:") {
		t.Error("disk mode must not attach an apkovl overlay")
	}
}

func TestArgsDiskInstalled(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "work", Mode: "disk", ISO: "isos/alpine.iso",
		RAM: 8192, CPUs: 4, Disk: "8G", Installed: true,
		Share: "/home/u/vms", SSHPort: 2202,
		Dir: filepath.Join("/data", "work"),
	}
	got := joined(Args(v))

	if strings.Contains(got, "-cdrom") || strings.Contains(got, "-boot d") {
		t.Errorf("installed disk VM must not boot the ISO:\n%s", got)
	}
}

func TestArgsCloud(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "cloudy", Mode: "cloud", Base: "/data/base/alpine.qcow2",
		RAM: 2048, CPUs: 2, Share: "/home/u/vms", SSHPort: 2204,
		Dir: filepath.Join("/data", "cloudy"),
	}
	got := joined(Args(v))

	if !strings.Contains(got, "-drive file=/data/cloudy/disk.qcow2,if=virtio") {
		t.Errorf("qcow2 overlay not booted:\n%s", got)
	}
	if !strings.Contains(got, "-drive file=/data/cloudy/ovl/seed.iso,media=cdrom") {
		t.Errorf("cloud-init seed not attached as cdrom:\n%s", got)
	}
	if strings.Contains(got, "-cdrom") {
		t.Error("cloud mode must not attach an install ISO")
	}
	if strings.Contains(got, "fat:rw:") {
		t.Error("cloud mode must not attach an apkovl overlay")
	}
	if !strings.Contains(got, "hostfwd=tcp:127.0.0.1:2204-:22") {
		t.Error("hostfwd must bind 127.0.0.1, not all interfaces")
	}
}

func TestArgsNoShare(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "bare", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2203, Dir: "/data/bare",
	}
	if strings.Contains(joined(Args(v)), "-virtfs") {
		t.Error("empty share must not produce a -virtfs flag")
	}
}
