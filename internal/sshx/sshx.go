// Package sshx runs commands inside guests over the forwarded SSH port.
package sshx

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
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

// connOptions returns the connection settings ssh(1) and scp(1) both accept
// with IDENTICAL syntax: host key checking, connect behaviour, and the
// identity file. This is the one place they live, so a caller building an
// scp invocation (core.CopyTo/CopyFrom) never has to re-decide
// StrictHostKeyChecking or find the private key path a second time — the
// exact kind of drift this package exists to prevent (see Args' comment).
//
// The port flag is deliberately NOT here: ssh takes "-p" and scp takes "-P"
// (capital — scp's lowercase -p means "preserve file times"), so it is the
// one option each caller must supply itself. See CopyArgs.
func connOptions() []string {
	return []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-i", keys.PrivatePath(),
	}
}

// Args returns the argv (excluding argv[0]) for ssh into v. Host key checks
// are off on purpose: this is a loopback forward to a VM stoat just built,
// and live VMs are recreated constantly.
func Args(v *config.VM, extra ...string) []string {
	a := append([]string{"-p", fmt.Sprint(v.SSHPort)}, connOptions()...)
	a = append(a, User(v)+"@127.0.0.1")
	return append(a, extra...)
}

// CopyArgs returns the argv (excluding argv[0]) for scp between the host and
// v's guest, sharing every connection setting Args does (see connOptions) —
// only the port flag differs, because scp's is capital -P.
//
// toRemote picks the direction: true puts the guest spec ("user@127.0.0.1:
// remotePath") on the right, as scp's destination (core.CopyTo); false puts
// it on the left, as scp's source (core.CopyFrom). localPath is always a
// bare host path — never quoted or otherwise rewritten, since it is scp's
// own argv element, not something re-parsed by a shell.
//
// -q suppresses scp's interactive progress meter: this argv is built for
// exec.CommandContext, never a terminal, and an unused flag doing nothing is
// better than stray meter output ending up captured as if it were an error.
func CopyArgs(v *config.VM, localPath, remotePath string, toRemote bool) []string {
	a := append([]string{"-P", fmt.Sprint(v.SSHPort), "-q"}, connOptions()...)
	remoteSpec := User(v) + "@127.0.0.1:" + remotePath
	if toRemote {
		return append(a, localPath, remoteSpec)
	}
	return append(a, remoteSpec, localPath)
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

// recipeShutdownGrace is how long a recipe's ssh process gets to exit after
// ctx is cancelled before it is killed outright. cmd.Cancel below sends
// SIGTERM instead of exec.CommandContext's default SIGKILL so ssh gets the
// chance to close its session (and let a remote shell it started react)
// cleanly; this bounds that grace period rather than waiting on it forever.
const recipeShutdownGrace = 5 * time.Second

// Provision runs each of v's recipes over ssh, streaming output to
// last-provision.log. The detail view tails that file on a ticker, so there
// is no channel plumbing between this and the UI.
//
// ctx cancels the in-flight ssh process, not just the caller's wait on it:
// each recipe runs under exec.CommandContext, so a cancelled apply actually
// kills ssh rather than leaving it running against the guest after Provision
// has returned. The initial wait for sshd is different: Wait is a local dial
// loop with no process to kill, so it is raced against ctx.Done() below
// instead of being made to accept a ctx itself — cancelling there simply lets
// Provision return early while that goroutine finishes on its own, bounded by
// WaitTimeout regardless.
func Provision(ctx context.Context, v *config.VM) (err error) {
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
	waitDone := make(chan error, 1)
	go func() { waitDone <- Wait(v, WaitTimeout) }()
	select {
	case <-ctx.Done():
		fmt.Fprintf(log, "CANCELLED: %v\n", ctx.Err())
		return ctx.Err()
	case err := <-waitDone:
		if err != nil {
			fmt.Fprintf(log, "FAILED: %v\n", err)
			return err
		}
	}

	for _, name := range v.Recipes {
		// Checked before each recipe, not only relied on via cmd.Run below: a
		// ctx cancelled between recipes must stop here rather than starting
		// one more ssh process it will only have to kill.
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(log, "CANCELLED: %v\n", err)
			return err
		}

		body, err := recipes.Read(name)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		fmt.Fprintf(log, "\n=== recipe %s ===\n", name)

		cmd := exec.CommandContext(ctx, "ssh", Args(v, "sh", "-s")...)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = recipeShutdownGrace
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			// ctx being the cause is reported honestly rather than as a plain
			// recipe FAILED: waitApplied (internal/core/wait.go) only ever
			// treats a final "done" line as success, so either wording leaves
			// it correctly "not applied" — but a human reading the log, or a
			// caller sniffing Logs' text, must not read a cancellation as if
			// the recipe itself had failed.
			if ctxErr := ctx.Err(); ctxErr != nil {
				fmt.Fprintf(log, "CANCELLED: recipe %s: %v\n", name, ctxErr)
				return ctxErr
			}
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return fmt.Errorf("recipe %s: %w", name, err)
		}
	}
	fmt.Fprintln(log, "\ndone")
	return nil
}
