// Package apkovl builds the Alpine overlay tarball that makes a live VM come
// up already networked and reachable over SSH.
//
// The Alpine initramfs scans block devices for *.apkovl.tar.gz at the root of
// a mountable filesystem, so this file is written into the VM's ovl/ directory
// and served to the guest by QEMU's vvfat.
package apkovl

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
)

const repositories = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/main\n" +
	"https://dl-cdn.alpinelinux.org/alpine/latest-stable/community\n"

const interfaces = "auto lo\niface lo inet loopback\n\nauto eth0\niface eth0 inet dhcp\n"

// tmpName is the in-progress build's filename, in the same directory vvfat
// exports to the guest. It must NOT match nlplug-findfs's *.apkovl.tar.gz*
// glob (note the trailing wildcard), otherwise a file orphaned by a kill or
// power loss mid-Build could be picked up as the overlay and unpack a
// truncated tarball.
const tmpName = ".stoat-ovl.tmp"

// legacyTmpName is the temp filename used before tmpName was introduced. It
// DOES match nlplug-findfs's *.apkovl.tar.gz* glob, so one left behind by a
// crash or power loss during an old build could still be picked up by the
// initramfs and unpacked as a truncated overlay.
const legacyTmpName = "stoat.apkovl.tar.gz.tmp"

// installScript is the local.d script that drives an unattended disk
// install: skip if a previous run already finished, else run setup-alpine
// against the baked answerfile, mark success on the 9p work share, and reboot
// into the newly installed disk.
//
// Output goes to /dev/ttyS0, the serial port stoat captures to console.log, so
// `stoat logs` and the detail screen's console pager show the install. Alpine
// live puts its own console on the VGA tty, which stoat does not capture, so
// without this redirect the install ran with no visible log at all.
//
// The skip gate is the /mnt/work/.installed marker, written only when
// setup-alpine SUCCEEDS. It is not blkid: busybox blkid in the live environment
// does not honor a single-device argument the way util-linux does, so it finds
// the vfat apkovl overlay (/dev/vdb) and the iso9660 CD and reports success on
// every disk, blank or not, which skipped the install forever. The marker also
// distinguishes the two reasons a disk VM boots uninstalled: a finished install
// (marker present, `i` toggled off, or diskWritten not yet seen) is left alone,
// while a failed or partial install (no marker) is retried rather than left
// stuck. On failure setup-alpine's own error stays on screen and the VM does
// not reboot or mark itself done.
const installScript = `#!/bin/sh
[ -c /dev/ttyS0 ] && exec >/dev/ttyS0 2>&1
if [ -f /mnt/work/.installed ]; then
	echo "stoat: install already recorded on the work share, skipping"
	exit 0
fi
echo "stoat: installing Alpine unattended, this takes a few minutes..."
# setup-alpine sources the answerfile but does not export ERASE_DISKS, so its
# setup-disk child never sees it and stops at the erase confirmation. Export it
# here so the child inherits it and repartitions /dev/vda unattended.
export ERASE_DISKS=/dev/vda
if setup-alpine -e -f /etc/stoat/answerfile; then
	echo "$(date -Iseconds)" > /mnt/work/.installed
	echo "stoat: install complete, rebooting into the disk"
	reboot
else
	echo "stoat: setup-alpine FAILED; leaving the installer up so you can inspect it (see the log above)"
fi
`

// installIssue is the getty banner (/etc/issue) shown on the VGA console while
// the install runs. The console output itself goes to the serial port, not
// here (see installScript's ttyS0 redirect), so the window would otherwise show
// only a bare login prompt with no sign that an unattended install is running.
const installIssue = `
    stoat is installing Alpine on this VM.
    It runs unattended and reboots itself when done. Do not log in.
    Watch progress from the host:  stoat logs <vm>

`

type builder struct {
	tw  *tar.Writer
	err error
}

func (b *builder) file(name string, mode int64, body string) {
	if b.err != nil {
		return
	}
	b.err = b.tw.WriteHeader(&tar.Header{
		Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
	})
	if b.err == nil && len(body) > 0 {
		_, b.err = b.tw.Write([]byte(body))
	}
}

func (b *builder) dir(name string, mode int64) {
	if b.err != nil {
		return
	}
	b.err = b.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Typeflag: tar.TypeDir})
}

func (b *builder) symlink(name, target string) {
	if b.err != nil {
		return
	}
	b.err = b.tw.WriteHeader(&tar.Header{
		Name: name, Linkname: target, Mode: 0o777, Typeflag: tar.TypeSymlink,
	})
}

// Build writes <v.OvlDir()>/stoat.apkovl.tar.gz, replacing any existing one.
func Build(v *config.VM) error {
	if err := keys.Ensure(); err != nil {
		return err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return err
	}
	hostPriv, hostPub, err := keys.GuestHostKey()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.OvlDir(), 0o755); err != nil {
		return err
	}

	out := filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz")
	tmp := filepath.Join(v.OvlDir(), tmpName)
	// Remove any temp file orphaned by a crash mid-Build before tmpName
	// existed: unlike tmpName, it matches the initramfs's glob.
	os.Remove(filepath.Join(v.OvlDir(), legacyTmpName))
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		os.Remove(tmp)
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	b := &builder{tw: tw}

	b.dir("etc", 0o755)
	b.dir("etc/apk", 0o755)
	b.dir("etc/network", 0o755)
	b.dir("etc/ssh", 0o755)
	b.dir("etc/runlevels", 0o755)
	b.dir("etc/runlevels/boot", 0o755)
	b.dir("etc/runlevels/default", 0o755)
	b.dir("root", 0o700)
	b.dir("root/.ssh", 0o700)

	// Present-but-empty: tells the initramfs to still add the default boot
	// services. Without it modloop never runs and the guest has no modules.
	b.file("etc/.default_boot_services", 0o644, "")

	b.file("etc/apk/repositories", 0o644, repositories)
	// Only packages present on the boot media may be listed here; the
	// initramfs installs from the ISO, not the network.
	b.file("etc/apk/world", 0o644, "alpine-base\nopenssh\n")

	b.file("etc/network/interfaces", 0o644, interfaces)
	b.symlink("etc/runlevels/boot/networking", "/etc/init.d/networking")
	b.symlink("etc/runlevels/default/sshd", "/etc/init.d/sshd")

	b.file("root/.ssh/authorized_keys", 0o600, pub+"\n")
	b.file("etc/ssh/ssh_host_ed25519_key", 0o600, hostPriv)
	b.file("etc/ssh/ssh_host_ed25519_key.pub", 0o644, hostPub)

	fstab := "/dev/cdrom /media/cdrom iso9660 noauto,ro 0 0\n"
	// Two exports (core-api.md §10.2). `host` is the user's directory,
	// read-only. `work` is stoat's per-VM scratch, writable. `work` always
	// exists; `host` exists only when the user configured a share.
	//
	// `ro` matches what QEMU enforces host-side. Without it the guest mounts
	// rw, `mount -o remount,rw` reports success, and writes fail EROFS
	// later with no explanation.
	//
	// `nofail`: some guest kernels have no 9p module at all (Debian's cloud
	// kernel doesn't). Without it an unmountable share holds up boot.
	fstab += "work /mnt/work 9p trans=virtio,version=9p2000.L,rw,_netdev,nofail 0 0\n"
	b.dir("mnt", 0o755)
	b.dir("mnt/work", 0o755)
	if v.Share != "" {
		fstab += "host /mnt/host 9p trans=virtio,version=9p2000.L,ro,_netdev,nofail 0 0\n"
		b.dir("mnt/host", 0o755)
	}
	// The initramfs's default-boot-services set doesn't include localmount,
	// so without this symlink the shares would only mount as a side effect
	// of a recipe running `mount -a`.
	b.symlink("etc/runlevels/boot/localmount", "/etc/init.d/localmount")
	b.file("etc/fstab", 0o644, fstab)

	if v.Mode == "disk" {
		// Every uninstalled disk VM auto-installs: setup-alpine runs from the
		// config-derived answerfile, the local.d script marks success on the
		// work share and reboots into the installed disk. The script refuses a
		// non-blank disk itself (see installScript), so re-baking it on a disk
		// that already holds data does not wipe it. etc/runlevels/default/local
		// makes the initramfs run local.d scripts at all; without it
		// stoat-install.start never runs.
		b.dir("etc/stoat", 0o755)
		b.file("etc/stoat/answerfile", 0o644, GenerateAnswerfile(v, pub))
		b.dir("etc/local.d", 0o755)
		b.file("etc/local.d/stoat-install.start", 0o755, installScript)
		b.symlink("etc/runlevels/default/local", "/etc/init.d/local")
		// The getty on the VGA console shows this instead of a bare login
		// prompt, so the window says an unattended install is running.
		b.file("etc/issue", 0o644, installIssue)
	}

	if b.err != nil {
		return b.err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}
