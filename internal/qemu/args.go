// Package qemu builds QEMU invocations and manages the resulting processes.
package qemu

import (
	"fmt"

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
	case "disk":
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
		if !v.Installed {
			a = append(a, "-cdrom", v.ISOPath(), "-boot", "d")
		}
	}
	return a
}
