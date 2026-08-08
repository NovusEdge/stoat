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
// It does not fall back to guest.DefaultSSHUser. An empty v.SSHUser is not
// "unknown": the cloudinit backend always seeds a real account, so it is
// empty only for the apkovl and ssh backends, both unlocked-root images (a
// live Alpine apkovl, or a BYO disk image awaiting a manual install).
// Guessing the registry default there would be wrong. A BYO file can be
// labelled e.g. "ubuntu" via iso.Infer or a form override while still going
// through the ssh backend, with no cloud-init and no seeded account, so that
// image has no "stoat" user, only whatever the installer itself created.
// See form.go's resolvedSSHUser for the one place that decides the recorded
// value.
func User(v *config.VM) string {
	if v.SSHUser != "" {
		return v.SSHUser
	}
	return "root"
}

// connOptions returns the connection settings ssh(1) and scp(1) both accept
// with identical syntax: host key checking, connect behaviour, and the
// identity file. Keeping them in one place means a caller building an scp
// invocation (core.CopyTo/CopyFrom) never has to re-decide
// StrictHostKeyChecking or find the private key path a second time.
//
// The port flag is not here. ssh takes "-p" and scp takes "-P" (capital,
// since scp's lowercase -p means "preserve file times"), so each caller
// supplies it itself. See CopyArgs.
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
// v's guest. It shares every connection setting Args does (see connOptions)
// and differs only in the port flag, since scp's is capital -P.
//
// toRemote picks the direction. true puts the guest spec
// ("user@127.0.0.1:remotePath") on the right, as scp's destination
// (core.CopyTo). false puts it on the left, as scp's source (core.CopyFrom).
// localPath is always a bare host path, never quoted or rewritten: it is
// scp's own argv element, not something a shell re-parses.
//
// -q suppresses scp's interactive progress meter. This argv is built for
// exec.CommandContext, never a terminal, so stray meter output would
// otherwise get captured as if it were an error.
func CopyArgs(v *config.VM, localPath, remotePath string, toRemote bool) []string {
	a := append([]string{"-P", fmt.Sprint(v.SSHPort), "-q"}, connOptions()...)
	remoteSpec := User(v) + "@127.0.0.1:" + remotePath
	if toRemote {
		return append(a, localPath, remoteSpec)
	}
	return append(a, remoteSpec, localPath)
}

// Wait blocks until the forwarded port accepts a connection, ctx is done, or
// timeout elapses, whichever comes first. timeout is a real ceiling even
// for a ctx with no deadline of its own, matching WaitTimeout's role in
// Provision below.
//
// ctx is honoured at two points, not just between attempts. The dial itself
// runs under DialContext, so a cancellation lands as soon as the connect
// syscall returns. The sleep between attempts is a select against
// ctx.Done(), not a bare time.Sleep. internal/core/wait.go's
// waitReachable/sshBannerUp run the same DialContext-based banner check
// independently of this one: core.Wait needs to keep going after ctx
// expires elsewhere, sshx.Wait needs a caller-supplied timeout ceiling, and
// duplicating the ~10-line dial is cheaper than reconciling those two
// different contracts.
func Wait(ctx context.Context, v *config.VM, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", v.SSHPort)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		dialTimeout := time.Second
		if remaining < dialTimeout {
			dialTimeout = remaining
		}
		c, err := dialCtx(ctx, addr, dialTimeout)
		if err == nil {
			if bannerReady(c, time.Until(deadline)) {
				return nil
			}
			c.Close()
		} else if ctx.Err() != nil {
			return ctx.Err()
		}

		remaining = time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := 500 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}
	}
	return fmt.Errorf("%s: ssh not reachable on port %d after %s", v.Name, v.SSHPort, timeout)
}

// dialCtx dials addr, bounding the attempt by whichever of ctx or
// perAttempt's own timeout fires first. A plain context.WithTimeout(ctx,
// perAttempt) would do the same, but this avoids the extra cancel func at
// every call site inside Wait's loop.
func dialCtx(ctx context.Context, addr string, perAttempt time.Duration) (net.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, perAttempt)
	defer cancel()
	var d net.Dialer
	return d.DialContext(dctx, "tcp", addr)
}

// bannerDeadline is the per-attempt read deadline for the SSH identification
// banner. A guest sshd forking under load on a 1-vCPU VM mid-boot can take
// longer than a couple hundred milliseconds to emit its banner. Each retry
// in Wait opens a fresh connection, so a too-short deadline here is a hard
// cliff no amount of retrying can cross. The outer WaitTimeout already
// bounds the whole operation, so a generous per-attempt deadline costs
// nothing in the failure case.
const bannerDeadline = 2 * time.Second

// bannerReady reports whether c is a real sshd, not just an accepted TCP
// connection. QEMU/libslirp's user-mode networking accepts the host-side
// socket at device init and only later dials the guest, tearing the
// connection down if nothing answers yet. A bare accept() does not mean
// sshd is up; requiring the "SSH-" identification banner does.
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
// SIGTERM instead of exec.CommandContext's default SIGKILL, so ssh gets the
// chance to close its session cleanly and let a remote shell it started
// react. This bounds that grace period rather than waiting on it forever.
const recipeShutdownGrace = 5 * time.Second

// Provision runs each of v's recipes over ssh, streaming output to
// last-provision.log. The detail view tails that file on a ticker, so there
// is no channel plumbing between this and the UI.
//
// ctx cancels both phases: the initial wait for sshd (Wait is itself
// ctx-aware) and each recipe's ssh process. Each recipe runs under
// exec.CommandContext, so a cancelled apply actually kills ssh rather than
// leaving it running against the guest after Provision has returned.
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
	if err := Wait(ctx, v, WaitTimeout); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintf(log, "CANCELLED: %v\n", err)
		} else {
			fmt.Fprintf(log, "FAILED: %v\n", err)
		}
		return err
	}

	for _, name := range v.Recipes {
		// Checked before each recipe, not left to cmd.Run below alone: a ctx
		// cancelled between recipes must stop here rather than start one more
		// ssh process it will only have to kill.
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(log, "CANCELLED: %v\n", err)
			return err
		}

		body, err := recipes.Read(name)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		fmt.Fprintf(log, "\n%s\n", RecipeMarker(name))

		cmd := exec.CommandContext(ctx, "ssh", Args(v, "sh", "-s")...)
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = recipeShutdownGrace
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			// ctx being the cause is reported as CANCELLED, not a plain recipe
			// FAILED. waitApplied (internal/core/wait.go) treats only a final
			// "done" line as success, so either wording leaves the recipe
			// correctly "not applied". But a human reading the log, or a
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
