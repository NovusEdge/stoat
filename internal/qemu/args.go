// Package qemu builds QEMU invocations and manages the resulting processes.
package qemu

import (
	"fmt"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
)

// Binary is the QEMU executable stoat drives.
const Binary = "qemu-system-x86_64"

// Args returns the argv (excluding argv[0]) for a VM. It is pure: the config
// must already be resolved, and nothing here touches the filesystem.
func Args(v *config.VM) []string {
	a := []string{
		"-enable-kvm",
		"-m", fmt.Sprint(v.RAM),
		"-smp", fmt.Sprint(v.CPUs),
		"-daemonize",
		"-pidfile", v.PidPath(),
		"-monitor", "unix:" + v.MonitorPath() + ",server,nowait",
		"-vga", "virtio",
		"-display", "gtk,gl=on",
		// Bind loopback explicitly: the QEMU default is 0.0.0.0, which would
		// publish every guest's SSH to the LAN.
		"-netdev", fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-:22", v.SSHPort),
		"-device", "virtio-net,netdev=n0",
	}

	if v.Share != "" {
		// security_model is mandatory; passthrough needs root and mapped-xattr
		// needs host xattr support, so none is the only unprivileged option.
		a = append(a, "-virtfs",
			fmt.Sprintf("local,path=%s,mount_tag=host,security_model=none", v.Share))
	}

	switch v.Mode {
	case "live":
		a = append(a, "-cdrom", v.ISOPath())
		// The initramfs looks for *.apkovl.tar.gz at the root of a mountable
		// filesystem, so the tarball is served through vvfat as a fake FAT disk.
		a = append(a, "-drive", "file=fat:rw:"+v.OvlDir()+",format=raw,if=virtio")
		// vvfat synthesizes a valid MBR signature but no boot code, so without
		// an explicit boot order QEMU's disk-first default can select the
		// empty overlay and hang instead of falling through to the ISO.
		a = append(a, "-boot", "d")
	case "disk":
		// The qcow2 comes first so it is vda: the installer's disk picker
		// lists devices in order, and the target must be the obvious answer.
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
		if !v.Installed {
			a = append(a, "-cdrom", v.ISOPath())
			// The same overlay a live VM gets, for the duration of the
			// install only. Alpine's setup-disk in sys mode copies the
			// RUNNING system onto the target, so an installer environment
			// that already has stoat's key in /root/.ssh ends up installing
			// it — otherwise the guest is unreachable after the install and
			// provisioning dies on "Permission denied (publickey)".
			//
			// Only Alpine's initramfs looks for an apkovl. Any other ISO
			// would just see a second, useless disk in its target picker.
			if v.OS == "alpine" {
				a = append(a, "-drive", "file=fat:rw:"+v.OvlDir()+",format=raw,if=virtio")
			}
			a = append(a, "-boot", "d")
		}
	case "cloud":
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
		// The xorriso seed has no El Torito boot record, so BIOS skips it and
		// boots the qcow2 without needing an explicit -boot order. cloud-init's
		// NoCloud datasource finds it by scanning cdrom devices for the CIDATA
		// volume label.
		seedISO := filepath.Join(v.OvlDir(), "seed.iso")
		a = append(a, "-drive", "file="+seedISO+",media=cdrom")
	}
	return a
}
