package qemu

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
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

// Start launches QEMU. -daemonize means it detaches itself; stoat supervises
// nothing and tracks the process by pidfile.
func Start(v *config.VM) error {
	if Running(v) {
		return fmt.Errorf("%s is already running", v.Name)
	}
	os.Remove(v.MonitorPath())
	if v.Mode == "live" {
		if err := os.MkdirAll(v.OvlDir(), 0o755); err != nil {
			return err
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
		return fmt.Errorf("qemu failed to start: %s", msg)
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
	if c, err := net.Dial("unix", v.MonitorPath()); err == nil {
		fmt.Fprintln(c, "system_powerdown")
		c.Close()
		for i := 0; i < 100; i++ {
			if !Running(v) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if p := pid(v); p != 0 {
		_ = syscall.Kill(p, syscall.SIGTERM)
	}
	return nil
}
