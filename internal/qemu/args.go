// Package qemu builds QEMU invocations and manages the resulting processes.
package qemu

import (
	"fmt"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/config"
)

// Binary is the QEMU executable stoat drives.
const Binary = "qemu-system-x86_64"

// The two surfaces a VM's screen can appear on. Also the wire values for the
// JSON DTO's "display" field, so the name a consumer reads is the same name
// this package decides.
const (
	DisplayWindow = "window"
	DisplayVNC    = "vnc"
)

// DisplayKind is the rule, stated over the three facts it actually depends on
// rather than over a *config.VM: core.VM is a different type that carries the
// first two, and it must ask this question rather than restate it.
//
// Only an uninstalled disk-mode VM wants a real window, because its OS
// installer draws to VGA rather than the serial console and a human has to
// drive it. live, cloud and installed disk VMs reach ssh with no console
// interaction, so their screen goes to the VNC socket instead.
//
// graphical is the host's veto, and it is a veto rather than a preference:
// -display gtk needs a display server on the HOST, and qemu does not degrade
// when there is none, it exits 1 (measured: "gtk initialization failed", and
// with gl=on it fails one option earlier still, on "OpenGL is not supported by
// display backend 'gtk'"). Before this argument existed, a fresh disk VM on a
// headless host could not be started at all, and the install could not be
// completed by any route. It falls back to VNC instead, which serves the exact
// same VGA framebuffer over a socket the user can reach from a machine that
// does have a screen. Whoever answers this must answer it for the host, not
// for the VM: see GraphicalSession.
//
// This is deliberately blind to whether the guest has a desktop on it. An
// installed disk VM running XFCE would like a window and does not get one; see
// docs/troubleshooting.md. Widening that needs an explicit per-VM preference,
// not a looser predicate.
func DisplayKind(mode string, installed, graphical bool) string {
	if mode == "disk" && !installed && graphical {
		return DisplayWindow
	}
	return DisplayVNC
}

// NeedsWindow reports whether this VM gets a real qemu window on a host whose
// graphical session is as given.
//
// Exported so the TUI can describe the right escape hatch (a GTK window vs.
// the VNC socket) without duplicating this rule.
func NeedsWindow(v *config.VM, graphical bool) bool {
	return DisplayKind(v.Mode, v.Installed, graphical) == DisplayWindow
}

// WantsWindow reports whether this VM would use a window if the host could
// give it one. It is the same rule with the host's veto removed, and it is
// what separates "no window because this VM never gets one" from "no window
// because this host has no session", which is the difference the user has to
// be told about.
func WantsWindow(v *config.VM) bool {
	return NeedsWindow(v, true)
}

// Args returns the argv (excluding argv[0]) for a VM. It is pure: the config
// must already be resolved, graphical is the caller's already-made decision
// about the host (GraphicalSession), and nothing here touches the filesystem
// or the environment.
func Args(v *config.VM, graphical bool) []string {
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

	if NeedsWindow(v, graphical) {
		a = append(a, "-vga", "virtio", "-display", "gtk,gl=on")
	} else {
		// -display none is irreversible on a running qemu, so bind VNC to a
		// socket at launch instead, recoverable for a misbehaving guest.
		// No -vga here, including for an install console that fell back to
		// this path: qemu's default adapter is what every VNC VM in stoat has
		// always used, and swapping in virtio for the fallback would put an
		// untested adapter under the one screen a human has to read.
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
