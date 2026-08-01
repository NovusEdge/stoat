package qemu

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

// Preflight reports why VMs cannot start, or nil if they can.
func Preflight() error {
	if _, err := exec.LookPath(Binary); err != nil {
		return fmt.Errorf("%s not found in PATH", Binary)
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("/dev/kvm not usable: %w (are you in the kvm group?)", err)
	}
	f.Close()
	return nil
}

func pid(v *config.VM) int {
	b, err := os.ReadFile(v.PidPath())
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return p
}

// cmdlineMatches reports whether a /proc/<pid>/cmdline blob belongs to the VM
// whose directory is dir. It anchors on dir+"/" rather than a bare substring
// match: cmdline always contains "-pidfile <dir>/qemu.pid", so the trailing
// separator is present for a genuine match, but a bare Contains would also
// match a sibling VM whose directory name has dir's as a prefix (e.g. "work"
// matching inside "work2").
func cmdlineMatches(cmdline []byte, dir string) bool {
	return bytes.Contains(cmdline, []byte(dir+"/"))
}

// Running reports whether this VM's QEMU process is alive. The cmdline check
// matters: pids are reused, and a stale pidfile would otherwise report a ghost.
func Running(v *config.VM) bool {
	p := pid(v)
	if p == 0 {
		return false
	}
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p))
	if err != nil {
		os.Remove(v.PidPath())
		return false
	}
	if !cmdlineMatches(cmdline, v.Dir) {
		os.Remove(v.PidPath())
		return false
	}
	return true
}

// Uptime returns how long the VM has been running, or 0 if it is stopped.
func Uptime(v *config.VM) time.Duration {
	if !Running(v) {
		return 0
	}
	fi, err := os.Stat(v.PidPath())
	if err != nil {
		return 0
	}
	return time.Since(fi.ModTime()).Truncate(time.Second)
}

// installedBytes is how much has to be written into a disk VM's qcow2 before
// stoat believes an OS landed in it. A freshly created 8G qcow2 is ~200 KB of
// metadata and nothing else; the smallest real install is three orders of
// magnitude past this, so the gap is not close.
//
// ponytail: a size check, not a partition-table read. If an install dies
// halfway and trips this anyway, "i" on the detail screen forces the ISO back.
const installedBytes = 10 << 20

// diskWritten reports whether v's disk image has had anything real written to
// it. qcow2 files grow as guest blocks are allocated, so the host-side size is
// the whole signal — no qemu-img, no NBD mount.
func diskWritten(v *config.VM) bool {
	fi, err := os.Stat(v.DiskPath())
	return err == nil && fi.Size() > installedBytes
}

// Start launches QEMU. -daemonize means it detaches itself; stoat supervises
// nothing and tracks the process by pidfile.
func Start(v *config.VM) error {
	if Running(v) {
		return fmt.Errorf("%s is already running", v.Name)
	}
	os.Remove(v.MonitorPath())
	// The interactive install happens inside the guest, where stoat cannot
	// watch it finish. Noticing it here — at the next start, which is the
	// only moment the boot order matters — means nobody has to report it.
	if v.Mode == "disk" && !v.Installed && diskWritten(v) {
		v.Installed = true
		if err := v.Save(); err != nil {
			v.Installed = false
			return fmt.Errorf("marking %s installed: %w", v.Name, err)
		}
		logx.L().Info("disk has an OS on it, marking installed", "vm", v.Name)
	}
	// A disk VM gets the same overlay while it is still booting its installer:
	// the key it plants in /root/.ssh is what setup-disk copies onto the
	// target, and without it the installed guest has no way to let stoat in.
	if v.Mode == "live" || (v.Mode == "disk" && !v.Installed && v.OS == "alpine") {
		// Disposable VMs: always rebuild rather than checking staleness.
		if err := apkovl.Build(v); err != nil {
			return fmt.Errorf("building apkovl: %w", err)
		}
	}
	if v.Mode == "cloud" {
		// Unlike the apkovl branch above, a cloud overlay holds real guest
		// state (installed packages, home directories, ...), so it is
		// created once and never rebuilt on later starts.
		if err := ensureCloudOverlay(v); err != nil {
			return fmt.Errorf("preparing cloud overlay: %w", err)
		}
	}
	cmd := exec.Command(Binary, Args(v)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		logx.L().Error("start failed", "vm", v.Name, "err", msg)
		return fmt.Errorf("qemu failed to start: %s", msg)
	}
	// Log how to get in, not just that it started: this is the line someone
	// greps for when a VM is up but they cannot reach it.
	user := v.SSHUser
	if user == "" {
		user = "root"
	}
	logx.L().Info("started",
		"vm", v.Name, "mode", v.Mode,
		"ssh", fmt.Sprintf("%s@127.0.0.1:%d", user, v.SSHPort),
		"console", consoleCredential(v, user))
	return nil
}

// consoleCredential describes how (or whether) a human can log in at the qemu
// window, for the start log line.
func consoleCredential(v *config.VM, user string) string {
	switch {
	case v.ConsolePassword != "":
		return user + "/" + v.ConsolePassword
	case v.Mode == "live":
		return "root, no password"
	case v.Mode == "cloud":
		return "none — accounts are locked, use ssh"
	default:
		return "whatever the guest installer was given"
	}
}

// ensureCloudOverlay creates v's CoW overlay (backed by v.Base) and cloud-init
// seed the first time a cloud-mode VM starts. It is a no-op on later starts:
// once created, the overlay accumulates real guest state (installed
// packages, home directories, ...) that must never be discarded the way a
// live VM's apkovl is rebuilt on every start.
func ensureCloudOverlay(v *config.VM) error {
	if _, err := os.Stat(v.DiskPath()); err == nil {
		return nil
	}
	base, err := filepath.Abs(v.Base)
	if err != nil {
		return err
	}
	out, err := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", v.DiskPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
	}
	// An overlay inherits its BASE image's virtual size, and cloud images are
	// sized to boot, not to install anything: Ubuntu 24.04's is 3.5G with a
	// 2.4G root. Installing a desktop into that fills the disk, apt exits 100,
	// and cloud-init reports a bare "error" — the failure looks like a broken
	// recipe rather than a full disk. Grow it here, before first boot, so
	// cloud-init's own growpart/resizefs expands the filesystem to match.
	if v.Disk != "" {
		out, err := exec.Command("qemu-img", "resize", v.DiskPath(), v.Disk).CombinedOutput()
		if err != nil {
			os.Remove(v.DiskPath())
			return fmt.Errorf("qemu-img resize to %s: %s", v.Disk, strings.TrimSpace(string(out)))
		}
	}
	if err := keys.Ensure(); err != nil {
		return err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return err
	}
	// v.Recipes only ever holds names the form offered for this VM's
	// os/backend, so for a cloud VM every entry here is already a cloud
	// fragment (recipes.List filters by backend at selection time) — no
	// extra backend check needed before reading them.
	var recipeBodies []string
	for _, name := range v.Recipes {
		body, err := recipes.Read(name)
		if err != nil {
			return fmt.Errorf("reading recipe %s: %w", name, err)
		}
		recipeBodies = append(recipeBodies, body)
	}
	if _, err := cloudinit.Seed(v, pub, recipeBodies); err != nil {
		return err
	}
	return nil
}

// Stop asks the guest to power down cleanly over the monitor socket, and falls
// back to SIGTERM. The fallback is a power cut: fine for live VMs, lossy for
// disk ones, which is why it is not the first move.
func Stop(v *config.VM) error {
	if !Running(v) {
		return nil
	}
	if c, err := dialMonitor(v); err == nil {
		fmt.Fprintln(c, "system_powerdown")
		c.Close()
		for i := 0; i < 100; i++ {
			if !Running(v) {
				logx.L().Info("stopped", "vm", v.Name)
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if p := pid(v); p != 0 {
		logx.L().Warn("graceful powerdown timed out, sending SIGTERM", "vm", v.Name, "pid", p)
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	return nil
}
