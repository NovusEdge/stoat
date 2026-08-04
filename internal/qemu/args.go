// Package qemu builds QEMU invocations and manages the resulting processes.
package qemu

import (
	"fmt"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/config"
)

// Binary is the QEMU executable stoat drives.
const Binary = "qemu-system-x86_64"

// NeedsWindow reports whether a human has to look at this VM's screen: only
// an uninstalled disk-mode VM, whose OS installer draws to VGA rather than
// the serial console. live, cloud, and installed disk VMs reach ssh with no
// console interaction.
//
// Exported so the TUI can describe the right escape hatch (a GTK window vs.
// the VNC socket) without duplicating this rule.
func NeedsWindow(v *config.VM) bool {
	return v.Mode == "disk" && !v.Installed
}

// Args returns the argv (excluding argv[0]) for a VM. It is pure: the config
// must already be resolved, and nothing here touches the filesystem.
func Args(v *config.VM) []string {
	// hostfwd is repeatable within a single -netdev's option string, so
	// additional forwards are appended clauses on the SSH netdev rather than
	// a second -netdev/-device pair.
	netdev := fmt.Sprintf("user,id=n0,hostfwd=tcp:127.0.0.1:%d-:22", v.SSHPort)
	for _, f := range v.Forwards {
		netdev += fmt.Sprintf(",hostfwd=tcp:127.0.0.1:%d-:%d", f.HostPort, f.GuestPort)
	}

	a := []string{
		"-enable-kvm",
		"-m", fmt.Sprint(v.RAM),
		"-smp", fmt.Sprint(v.CPUs),
		"-daemonize",
		"-pidfile", v.PidPath(),
		"-monitor", "unix:" + v.MonitorPath() + ",server,nowait",
		// QMP alongside the human monitor, not replacing it: Stop and
		// TypeConsolePassword still use the monitor, QMP is for snapshots.
		// ",server,nowait" means QEMU doesn't block waiting for a connection.
		"-qmp", "unix:" + v.QMPPath() + ",server,nowait",
		// Serial console, captured unconditionally as the only postmortem for
		// a headless VM. What lands here depends on the guest's console=
		// setting; Alpine's cloud images only send early kernel messages.
		// append=on because plain "-serial file:<path>" truncates on every
		// start, destroying the log on the next restart after a failed boot.
		"-chardev", "file,id=con0,path=" + v.ConsoleLogPath() + ",append=on",
		"-serial", "chardev:con0",
		// Bind loopback explicitly: QEMU's default is 0.0.0.0, which would
		// publish SSH and every forward to the LAN.
		"-netdev", netdev,
		"-device", "virtio-net,netdev=n0",
	}

	if NeedsWindow(v) {
		a = append(a, "-vga", "virtio", "-display", "gtk,gl=on")
	} else {
		// -display none is irreversible on a running qemu, so bind VNC to a
		// socket at launch instead, recoverable for a misbehaving guest.
		a = append(a, "-display", "none", "-vnc", "unix:"+v.VNCPath())
	}

	// Two exports (core-api.md §10.2): the user's dir read-only, stoat's
	// per-VM scratch writable.
	//
	// mapped-xattr uses the unprivileged user.* namespace, and stops a
	// guest-created symlink from being a real host symlink (under `none` it
	// is one, and a host process walking the share follows it out).
	//
	// readonly=on is enforced host-side: `mount -o remount,rw` succeeds in the
	// guest but writes still fail EROFS. readonly=1 is a parse error.
	//
	// -virtfs derives the fsdev id from mount_tag, so the tags must differ.
	if v.Share != "" {
		a = append(a, "-virtfs",
			fmt.Sprintf("local,path=%s,mount_tag=host,security_model=mapped-xattr,readonly=on", v.Share))
	}
	a = append(a, "-virtfs",
		fmt.Sprintf("local,path=%s,mount_tag=work,security_model=mapped-xattr", v.WorkDir()))

	// Mode owns the boot media: the qcow2, the installer ISO, the boot
	// order. Provisioning is the backend's job, below.
	switch v.Mode {
	case "live":
		a = append(a, "-cdrom", v.ISOPath())
		// vvfat synthesizes a valid MBR signature but no boot code, so without
		// an explicit boot order QEMU's disk-first default can select the
		// empty apkovl overlay and hang instead of falling through to the ISO.
		a = append(a, "-boot", "d")
	case "disk":
		// qcow2 comes first so it is vda: the installer's disk picker lists
		// devices in order and the target must be the obvious answer. The
		// backend's drive goes on the end, after this.
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
		if !v.Installed {
			a = append(a, "-cdrom", v.ISOPath())
			a = append(a, "-boot", "d")
		}
	case "cloud":
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
	}

	// The provisioning artifact's drive: the apkovl overlay, the cloud-init
	// seed, or nothing, per backend.For(v) rather than v.OS. That is what
	// makes alpine-cloud (OS "alpine", backend "cloudinit") resolve to the
	// right one instead of an apkovl it can't boot from.
	//
	// Safe to append last: the seed is a cdrom, and the overlay is only ever
	// a second virtio disk behind the installer ISO or the qcow2 (vda).
	return append(a, backend.For(v).Args(v)...)
}
