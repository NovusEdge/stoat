// Package sshx runs commands inside guests over the forwarded SSH port.
package sshx

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

// WaitTimeout is how long Provision waits for sshd after a start. A first
// live boot plus dhcp is slower than a warm one, so this is generous.
const WaitTimeout = 90 * time.Second

// User returns the account to ssh into v as: v.SSHUser when it was recorded
// at build time (the catalog entry's account, or cloudinit.User for a cloud
// image), otherwise "root".
//
// This deliberately does NOT consult guest.DefaultSSHUser as a second
// fallback. An empty v.SSHUser is not "unknown" — for the cloudinit backend
// it seeds a real account, so it is only ever empty for the apkovl/ssh
// backends, both of which are unlocked-root images (a live Alpine apkovl, or
// a BYO disk image awaiting a manual install). Guessing an OS's registry
// default there would be wrong: a BYO file can be labelled e.g. "ubuntu" via
// iso.Infer or a form override while still going through the ssh backend
// (no cloud-init, no seeded account), and that image has no "stoat" user —
// only whatever the installer itself created. See form.go's
// resolvedSSHUser for the one place that decides the recorded value.
func User(v *config.VM) string {
	if v.SSHUser != "" {
		return v.SSHUser
	}
	return "root"
}

// Args returns the argv (excluding argv[0]) for ssh into v. Host key checks
// are off on purpose: this is a loopback forward to a VM stoat just built,
// and live VMs are recreated constantly.
func Args(v *config.VM, extra ...string) []string {
	a := []string{
		"-p", fmt.Sprint(v.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-i", keys.PrivatePath(),
		User(v) + "@127.0.0.1",
	}
	return append(a, extra...)
}

// Wait blocks until the forwarded port accepts a connection, or timeout.
func Wait(v *config.VM, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", v.SSHPort)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		dialTimeout := time.Second
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		c, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err == nil {
			if bannerReady(c, time.Until(deadline)) {
				return nil
			}
			c.Close()
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := 500 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
	return fmt.Errorf("%s: ssh not reachable on port %d after %s", v.Name, v.SSHPort, timeout)
}

// bannerDeadline is the per-attempt read deadline for the SSH identification
// banner. A guest sshd forking under load on a 1-vCPU VM mid-boot can
// plausibly take longer than a couple hundred milliseconds to emit its
// banner; since each retry in Wait opens a fresh connection, a too-short
// deadline here is a hard cliff that no amount of retrying can cross. The
// outer WaitTimeout already bounds the whole operation, so a generous
// per-attempt deadline costs nothing in the failure case.
const bannerDeadline = 2 * time.Second

// bannerReady reports whether c is a real sshd, not just an accepted TCP
// connection. QEMU/libslirp's user-mode networking accepts the host-side
// socket at device init and only later dials the guest, tearing the
// connection down if nothing answers there yet — so a bare accept() does
// not mean sshd is up. Requiring the "SSH-" identification banner does.
//
// budget caps the read deadline at the remaining overall timeout, so a
// short-lived caller (e.g. Wait(v, 300*time.Millisecond) in a test) still
// returns promptly instead of blocking for the full bannerDeadline.
func bannerReady(c net.Conn, budget time.Duration) bool {
	d := bannerDeadline
	if budget < d {
		d = budget
	}
	c.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 4)
	_, err := io.ReadFull(c, buf)
	return err == nil && string(buf) == "SSH-"
}

// Provision runs each of v's recipes over ssh, streaming output to
// last-provision.log. The detail view tails that file on a ticker, so there
// is no channel plumbing between this and the UI.
func Provision(v *config.VM) (err error) {
	logx.L().Info("provision start", "vm", v.Name, "recipes", strings.Join(v.Recipes, ","))
	defer func() {
		if err != nil {
			logx.L().Error("provision failed", "vm", v.Name, "err", err)
		} else {
			logx.L().Info("provision done", "vm", v.Name)
		}
	}()

	log, err := os.Create(v.ProvisionLogPath())
	if err != nil {
		return err
	}
	defer log.Close()

	fmt.Fprintf(log, "waiting for ssh on port %d…\n", v.SSHPort)
	if err := Wait(v, WaitTimeout); err != nil {
		fmt.Fprintf(log, "FAILED: %v\n", err)
		return err
	}

	for _, name := range v.Recipes {
		body, err := recipes.Read(name)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		fmt.Fprintf(log, "\n=== recipe %s ===\n", name)

		cmd := exec.Command("ssh", Args(v, "sh", "-s")...)
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return fmt.Errorf("recipe %s: %w", name, err)
		}
	}
	fmt.Fprintln(log, "\ndone")
	return nil
}
