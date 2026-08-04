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
		OS: "alpine", Backend: "apkovl",
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
		"-virtfs local,path=/home/u/vms,mount_tag=host,security_model=mapped-xattr,readonly=on",
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
		Name: "work", Mode: "disk", OS: "alpine", ISO: "isos/alpine.iso",
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
	// The overlay is what carries stoat's key into the installer environment,
	// so that setup-disk copies it onto the target. Without it the installed
	// guest refuses every key stoat has and provisioning can never work.
	if !strings.Contains(got, "fat:rw:/data/work/ovl") {
		t.Errorf("an alpine install needs the apkovl overlay for its key:\n%s", got)
	}
	// ...and it must come after the target disk, so the installer's picker
	// offers the qcow2 as vda rather than the overlay.
	if strings.Index(got, "disk.qcow2") > strings.Index(got, "fat:rw:") {
		t.Errorf("the overlay outranks the target disk:\n%s", got)
	}

	// A BYO ISO's installer has no idea what an apkovl is; the overlay would
	// only show up in its disk picker as a second, wrong target.
	v.OS = "fedora"
	if got := joined(Args(v)); strings.Contains(got, "fat:rw:") {
		t.Errorf("a non-alpine install was given an apkovl overlay:\n%s", got)
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
		OS: "alpine", Backend: "cloudinit",
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
	if strings.Contains(got, "-boot") {
		t.Error("cloud mode must not force-boot the seed cdrom")
	}
	if strings.Contains(got, "fat:rw:") {
		t.Error("cloud mode must not attach an apkovl overlay")
	}
	if !strings.Contains(got, "hostfwd=tcp:127.0.0.1:2204-:22") {
		t.Error("hostfwd must bind 127.0.0.1, not all interfaces")
	}
}

// Every VM writes its guest console to a file, in every mode. When an
// automated VM fails at 3am there is no window to look at and no operator
// watching -- the log on disk is the whole postmortem.
func TestArgsAlwaysLogTheConsole(t *testing.T) {
	for _, mode := range []string{"live", "disk", "cloud"} {
		v := &config.VM{Name: "vm", Dir: t.TempDir(), Mode: mode, RAM: 1024, CPUs: 2, SSHPort: 2222}
		got := strings.Join(Args(v), " ")
		if !strings.Contains(got, v.ConsoleLogPath()) {
			t.Errorf("mode=%s does not log the console:\n%s", mode, got)
		}
	}
}

// -serial file:<path> TRUNCATES on every QEMU start (verified against real
// QEMU: a pre-filled console.log became 0 bytes on next launch). The
// realistic failure sequence is exactly the one this log exists for -- a
// headless VM fails to come up, the user restarts it, and the only evidence
// is destroyed before anyone reads it. -chardev file,...,append=on is the
// fix; plain "-serial file:" must never come back.
func TestConsoleLogAppendsRatherThanTruncates(t *testing.T) {
	v := &config.VM{Name: "vm", Dir: t.TempDir(), Mode: "cloud", RAM: 1024, CPUs: 2, SSHPort: 2222}
	got := strings.Join(Args(v), " ")

	if strings.Contains(got, "-serial file:") {
		t.Errorf("-serial file: truncates the console log on every start:\n%s", got)
	}
	wantChardev := "-chardev file,id=con0,path=" + v.ConsoleLogPath() + ",append=on"
	if !strings.Contains(got, wantChardev) {
		t.Errorf("want chardev %q in:\n%s", wantChardev, got)
	}
	if !strings.Contains(got, "-serial chardev:con0") {
		t.Errorf("want -serial chardev:con0 in:\n%s", got)
	}
}

// live and cloud are automated end to end: sshd comes up with the user's key
// already installed, and nobody ever needs to touch the console. The window
// only steals focus. An installed disk VM is the same. But an UNINSTALLED
// disk VM is a human running a VGA-first OS installer -- take its window away
// and the mode becomes unusable.
func TestOnlyManualInstallsGetAWindow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		vm          config.VM
		wantsWindow bool
	}{
		{"live is automated", config.VM{Mode: "live"}, false},
		{"cloud is automated", config.VM{Mode: "cloud"}, false},
		{"installed disk boots like the others", config.VM{Mode: "disk", Installed: true}, false},
		{"uninstalled disk is a manual install", config.VM{Mode: "disk"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.vm
			v.Name, v.Dir, v.RAM, v.CPUs, v.SSHPort = "vm", t.TempDir(), 1024, 2, 2222
			got := strings.Join(Args(&v), " ")

			// A bare Contains(got, "gtk") passes on a malformed display
			// string too (e.g. a typo'd "-display gtk-broken,gl=on" or a
			// stray "gtk" in an unrelated flag). Pin the exact display
			// arguments both forms must produce instead.
			windowed := "-vga virtio -display gtk,gl=on"
			headless := "-display none -vnc unix:" + v.VNCPath()

			if tc.wantsWindow {
				if !strings.Contains(got, windowed) {
					t.Errorf("want exact windowed display args %q in:\n%s", windowed, got)
				}
				if strings.Contains(got, "-display none") || strings.Contains(got, "-vnc") {
					t.Errorf("a windowed VM must not also bind vnc or -display none:\n%s", got)
				}
				return
			}

			if !strings.Contains(got, headless) {
				t.Errorf("want exact headless display args %q in:\n%s", headless, got)
			}
			if strings.Contains(got, "-vga") || strings.Contains(got, "gtk") {
				t.Errorf("a headless VM must not also open a gtk window:\n%s", got)
			}
		})
	}
}

// TestArgsPortForwards pins the exact hostfwd syntax additional forwards
// must produce: additional comma-separated hostfwd= clauses on the SAME
// -netdev as the SSH forward, never a second -netdev/-device pair. Verified
// live against real qemu-system-x86_64 (see the comment in Args) that two
// hostfwd= clauses on one -netdev each become an independent host listener.
func TestArgsPortForwards(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "web", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2201,
		Forwards: []config.PortForward{
			{HostPort: 8080, GuestPort: 80},
			{HostPort: 8443, GuestPort: 443},
		},
		Dir: "/data/web",
	}
	got := joined(Args(v))

	want := "-netdev user,id=n0,hostfwd=tcp:127.0.0.1:2201-:22,hostfwd=tcp:127.0.0.1:8080-:80,hostfwd=tcp:127.0.0.1:8443-:443"
	if !strings.Contains(got, want) {
		t.Errorf("want exact netdev arg %q in:\n%s", want, got)
	}
	// Only one -netdev/-device pair total: forwards ride the existing NIC,
	// they do not add a second one.
	if strings.Count(got, "-netdev") != 1 || strings.Count(got, "-device virtio-net") != 1 {
		t.Errorf("want exactly one netdev/device pair, got:\n%s", got)
	}
}

// TestArgsNoForwardsUnchanged pins that a VM with no declared forwards
// produces exactly the SSH-only hostfwd this codebase had before this
// field existed -- a VM with Forwards == nil must not regress.
func TestArgsNoForwardsUnchanged(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "bare", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2201, Dir: "/data/bare",
	}
	got := joined(Args(v))
	want := "-netdev user,id=n0,hostfwd=tcp:127.0.0.1:2201-:22 "
	if !strings.Contains(got, want) {
		t.Errorf("want exact netdev arg %q in:\n%s", want, got)
	}
}

// A VM with no configured share still gets the writable `work` export, but
// must not get a `host` one. Every VM has somewhere to exchange files;
// only the read-only view of the user's own directory is optional.
func TestArgsNoShareStillGetsWork(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "bare", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2203, Dir: "/data/bare",
	}
	got := joined(Args(v))
	want := "-virtfs local,path=/data/shared/bare,mount_tag=work,security_model=mapped-xattr"
	if !strings.Contains(got, want) {
		t.Errorf("want %q in:\n%s", want, got)
	}
	if strings.Contains(got, "mount_tag=host") {
		t.Error("empty share must not produce a host export")
	}
}

// The host export is read-only and the work export is not. This asymmetry is
// the security model (core-api.md §10.2), so it is pinned exactly rather than
// checked loosely: readonly=on is what stops a guest writing to the user's
// own files, and mapped-xattr is what stops a guest-created symlink becoming
// a real host symlink.
func TestArgsShareExportsAreAsymmetric(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{
		Name: "shared", Mode: "live", ISO: "isos/alpine.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2203, Dir: "/data/shared-vm",
		Share: "/home/u/vms",
	}
	got := joined(Args(v))
	for _, want := range []string{
		"-virtfs local,path=/home/u/vms,mount_tag=host,security_model=mapped-xattr,readonly=on",
		"-virtfs local,path=/data/shared/shared-vm,mount_tag=work,security_model=mapped-xattr",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "security_model=none") {
		t.Error("security_model=none leaves guest-created symlinks as real host symlinks")
	}
	if strings.Contains(got, "mount_tag=work,security_model=mapped-xattr,readonly=on") {
		t.Error("the work export must be writable")
	}
}
