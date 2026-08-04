package qemu

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/logx"
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
// metadata and nothing else; the smallest real install is well past this.
//
// ponytail: a size check, not a partition-table read. If an install dies
// halfway and trips this anyway, "i" on the detail screen forces the ISO back.
const installedBytes = 10 << 20

// diskWritten reports whether v's disk image has had anything real written to
// it. qcow2 files grow as guest blocks are allocated, so the host-side size is
// the whole signal, with no qemu-img and no NBD mount needed.
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
	// The interactive install happens inside the guest, where stoat can't
	// watch it finish. Checked here, at the next start, since that's the
	// only moment the boot order matters.
	if v.Mode == "disk" && !v.Installed && diskWritten(v) {
		v.Installed = true
		if err := v.Save(); err != nil {
			v.Installed = false
			return fmt.Errorf("marking %s installed: %w", v.Name, err)
		}
		logx.L().Info("disk has an OS on it, marking installed", "vm", v.Name)
	}
	// Build whatever pre-boot artifact this VM's backend needs: the apkovl
	// overlay, or the cloud-init seed (cloud mode, first start only). Each
	// backend decides for itself whether there's anything to do.
	if err := backend.For(v).Prepare(v); err != nil {
		return fmt.Errorf("preparing %s: %w", v.Name, err)
	}
	// Before QEMU, not inside Args: a missing export path is fatal, and Args
	// is pure by contract.
	if err := prepareShares(v); err != nil {
		return err
	}
	// The one impure input Args refuses to look up for itself: whether this
	// host has a display server at all. Resolved here, once, per start.
	graphical := GraphicalSession()
	cmd := exec.Command(Binary, Args(v, graphical)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		logx.L().Error("start failed", "vm", v.Name, "err", msg)
		return fmt.Errorf("qemu failed to start: %s%s", msg, explainDisplayFailure(msg))
	}
	// Log how to get in, not just that it started. This is the line someone
	// greps for when a VM is up but unreachable.
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

// explainDisplayFailure returns the sentence to append to a qemu start failure
// whose message is about the window, or "" for any other failure.
//
// Both strings are qemu's, measured on a host with no session: `-display
// gtk,gl=on` reports "OpenGL is not supported by display backend 'gtk'" and
// plain `-display gtk` reports "gtk initialization failed". The first one is
// the reason this exists. It sends the reader to mesa and GPU drivers, when
// what is actually true is that the window cannot be opened at all, and gl=on
// is merely the option qemu rejected first.
//
// Reaching here means a window was asked for and refused, since GraphicalSession
// saying no puts the VM on VNC and never asks. That is the third case: a real
// session whose GTK build has no working GL. Nothing in the environment shows
// it in advance, so it is named here, after the fact, along with the override
// that works around it.
func explainDisplayFailure(msg string) string {
	if !strings.Contains(msg, "gtk") && !strings.Contains(msg, "OpenGL") {
		return ""
	}
	return "\nthis is about the qemu window, not your GPU: qemu could not open one on this host." +
		"\nrun with " + GraphicalEnv + "=0 to put the screen on this VM's VNC socket instead"
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
		return "none (accounts are locked, use ssh)"
	default:
		return "whatever the guest installer was given"
	}
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
