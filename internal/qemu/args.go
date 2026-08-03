// Package qemu builds QEMU invocations and manages the resulting processes.
package qemu

import (
	"fmt"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/config"
)

// Binary is the QEMU executable stoat drives.
const Binary = "qemu-system-x86_64"

// NeedsWindow reports whether a human has to look at this VM's screen. Only
// one case does: a disk-mode VM that has not been installed yet, where the
// user is driving an OS installer that draws to VGA and would be invisible
// on a serial console. live and cloud reach ssh with no console interaction
// at all, and an installed disk VM boots the same way -- for those the
// window only opens, steals focus, and gets alt-tabbed away from.
//
// Exported so the TUI can describe the right escape hatch (a GTK window vs.
// the VNC socket) without duplicating this rule.
func NeedsWindow(v *config.VM) bool {
	return v.Mode == "disk" && !v.Installed
}

// Args returns the argv (excluding argv[0]) for a VM. It is pure: the config
// must already be resolved, and nothing here touches the filesystem.
func Args(v *config.VM) []string {
	// hostfwd is repeatable within a single -netdev's option string: QEMU's
	// user-mode networking accepts any number of comma-separated
	// "hostfwd=[tcp|udp]:[hostaddr]:hostport-[guestaddr]:guestport" clauses
	// on one netdev instance, each opening its own host listener into the
	// guest. Verified directly, not just read off docs: launched real
	// qemu-system-x86_64 11.0.2 with
	// "-netdev user,id=n0,hostfwd=tcp:127.0.0.1:19922-:22,hostfwd=tcp:127.0.0.1:19980-:80"
	// and confirmed with `ss -ltn` that BOTH 127.0.0.1:19922 and
	// 127.0.0.1:19980 came up as independent host listeners from that one
	// netdev. This is why additional forwards are appended clauses on the
	// SSH netdev rather than a second -netdev/-device pair -- one guest
	// NIC, N forwarded ports, matching a router's port-forwarding table.
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
		// The guest's serial console, captured unconditionally. An automated VM
		// has no window and no operator watching it, so this file is the only
		// postmortem when a boot fails. Cheap: QEMU writes it whether or not
		// anything ever reads it.
		//
		// Note this is the SERIAL console, not the VGA one the display shows.
		// What lands here depends entirely on the guest's console= setting:
		// Alpine's generic cloud images point their console at tty0, so this
		// file gets only early kernel messages, not the full boot. Still, it's
		// the first place to look when a guest never comes up -- an empty log
		// here is a real, non-buggy outcome for some guests, not a sign this
		// broke.
		//
		// "-serial file:<path>" TRUNCATES on every start (verified against
		// real QEMU). The realistic sequence this log exists for is a
		// headless VM failing to come up and the user restarting it -- with
		// plain file: that restart destroys the only evidence before anyone
		// reads it. append=on on an explicit chardev keeps it.
		"-chardev", "file,id=con0,path=" + v.ConsoleLogPath() + ",append=on",
		"-serial", "chardev:con0",
		// Bind loopback explicitly: the QEMU default is 0.0.0.0, which would
		// publish every guest's SSH (and every user-declared forward) to the
		// LAN.
		"-netdev", netdev,
		"-device", "virtio-net,netdev=n0",
	}

	if NeedsWindow(v) {
		a = append(a, "-vga", "virtio", "-display", "gtk,gl=on")
	} else {
		// -display none is irreversible on a running qemu, so bind VNC to a
		// socket at launch. It costs nothing when unused and means the
		// display is always recoverable for a guest that misbehaves.
		a = append(a, "-display", "none", "-vnc", "unix:"+v.VNCPath())
	}

	if v.Share != "" {
		// security_model is mandatory; passthrough needs root and mapped-xattr
		// needs host xattr support, so none is the only unprivileged option.
		a = append(a, "-virtfs",
			fmt.Sprintf("local,path=%s,mount_tag=host,security_model=none", v.Share))
	}

	// Mode owns the BOOT MEDIA -- the qcow2, the installer ISO, the boot
	// order. It deliberately does not decide anything about how the guest is
	// provisioned; that is the backend's, below.
	switch v.Mode {
	case "live":
		a = append(a, "-cdrom", v.ISOPath())
		// vvfat synthesizes a valid MBR signature but no boot code, so without
		// an explicit boot order QEMU's disk-first default can select the
		// empty overlay the apkovl backend attaches and hang instead of
		// falling through to the ISO.
		a = append(a, "-boot", "d")
	case "disk":
		// The qcow2 comes first so it is vda: the installer's disk picker
		// lists devices in order, and the target must be the obvious answer.
		// Everything appended after this point is a later device, which is
		// why the backend's drive goes on the end rather than inline here.
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
		if !v.Installed {
			a = append(a, "-cdrom", v.ISOPath())
			a = append(a, "-boot", "d")
		}
	case "cloud":
		a = append(a, "-drive", "file="+v.DiskPath()+",if=virtio")
	}

	// The provisioning artifact's drive: the apkovl overlay, the cloud-init
	// seed, or nothing. Appended last, and unconditionally -- each backend
	// decides for itself which of v's modes it applies to, so this single
	// call covers the vvfat overlay a live boot and an uninstalled disk VM
	// both need, and the seed cdrom a cloud VM needs.
	//
	// Asking the backend rather than comparing v.OS == "alpine" is also what
	// makes alpine-cloud resolve correctly: it is OS "alpine" but backend
	// "cloudinit" (see internal/backend.For), so an OS comparison would hand
	// it an apkovl it does not boot from.
	//
	// Order is safe to leave until the end: the seed is a cdrom, and the
	// overlay is only ever a second virtio disk behind either the installer
	// ISO or the qcow2 that must stay vda.
	return append(a, backend.For(v).Args(v)...)
}
