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
			hasGTK := strings.Contains(got, "gtk")
			if hasGTK != tc.wantsWindow {
				t.Errorf("gtk window = %v, want %v:\n%s", hasGTK, tc.wantsWindow, got)
			}
			if tc.wantsWindow {
				return
			}
			if !strings.Contains(got, "-display none") && !strings.Contains(got, "display none") {
				t.Errorf("headless VM does not ask for -display none:\n%s", got)
			}
			// -display none cannot be undone on a running qemu, so the
			// display has to stay reachable some other way.
			if !strings.Contains(got, "vnc") {
				t.Errorf("headless VM has no way to get a display back:\n%s", got)
			}
		})
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
