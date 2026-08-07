package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// Until is a state Wait can block for. It is a small, closed set distinct
// from State (design doc §3): a caller waits for an EVENT ("this VM has
// become reachable"), not one of List/Get's six point-in-time labels.
// Applying and Failed are not events Wait resolves against; see waitApplied
// for why "applied" is answered from the apply log, not from a State this
// package does not yet produce.
type Until string

const (
	// UntilReachable is sshd answering on the VM's forwarded port, the same
	// signal sshx.Wait and Provision already treat as "up".
	UntilReachable Until = "reachable"
	// UntilApplied is the most recent recipe run having finished
	// successfully. See waitApplied.
	UntilApplied Until = "applied"
	// UntilStopped is qemu.Running turning false.
	UntilStopped Until = "stopped"
)

// ErrCannotReach is returned by Wait for a VM that cannot, by construction,
// ever reach the requested state: a stopped VM asked to become Reachable, or
// a VM with no recipes asked to become Applied. Wait checks for these cases
// up front. Otherwise a caller's programming mistake would hang until its
// own deadline with no useful message.
//
// ErrCannotReach is narrower than "will probably never happen". A running VM
// that never brings up sshd, or a VM whose recipes stall, is not reported
// this way: both remain genuinely possible until ctx says otherwise. Only a
// state nothing could ever move the VM into gets ErrCannotReach.
var ErrCannotReach = errors.New("vm cannot reach the requested state")

// pollInterval is how often Wait re-checks state once a fast-path signal
// (a listening socket, ctx cancellation) is not available. It is a
// compromise, not a tuned value: fast enough that a caller waiting
// interactively (the TUI) does not visibly stall, slow enough not to spin.
const pollInterval = 300 * time.Millisecond

// Wait blocks until VM name reaches the state described by until, or ctx is
// cancelled or hits its deadline, whichever comes first.
//
// Wait exists separately from Start's own wait flag (StartOpts.Wait in the
// design doc) because a caller does not always control when the VM started:
// it may have been started by an earlier call, by another process, or found
// already mid-boot by List. Wait lets that caller block on the outcome
// instead of re-polling Get in a loop.
func Wait(ctx context.Context, name string, until Until) error {
	v, err := load(name)
	if err != nil {
		return err
	}
	switch until {
	case UntilReachable:
		return waitReachable(ctx, v)
	case UntilApplied:
		return waitApplied(ctx, v)
	case UntilStopped:
		return waitStopped(ctx, v)
	default:
		return fmt.Errorf("%w: unknown Until %q", ErrInvalidSpec, until)
	}
}

// waitReachable blocks until sshd answers on v's forwarded port.
//
// A VM whose qemu process is not running is refused immediately, not
// polled. Wait never starts a VM itself; that is Start's job. A VM that is
// already not running can never bring sshd up on its own.
func waitReachable(ctx context.Context, v *config.VM) error {
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s: not running", ErrCannotReach, v.Name)
	}
	return pollUntil(ctx, func() bool { return sshBannerUp(ctx, v) })
}

// sshBannerUp reports whether v's forwarded port answers as a real sshd, not
// merely accepting TCP. QEMU/libslirp's user-mode networking accepts the
// host-side socket before the guest is dialled, so a bare accept() proves
// nothing. sshBannerUp requires the "SSH-" identification banner, the same
// check sshx.Wait's bannerReady makes. It is a ctx-aware reimplementation,
// not a call to bannerReady, because sshx.Wait takes a fixed timeout and
// cannot give up early when ctx is cancelled mid-dial.
func sshBannerUp(ctx context.Context, v *config.VM) bool {
	d := net.Dialer{Timeout: time.Second}
	c, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", v.SSHPort))
	if err != nil {
		return false
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	return err == nil && string(buf) == "SSH-"
}

// waitApplied blocks until v's most recent recipe run has finished
// successfully.
//
// The signal is the apply log. sshx.Provision truncates last-provision.log
// at the start of every run (os.Create) and writes a bare "done" as its
// final line only on success. internal/tui/autoprov.go's
// lastProvisionSucceeded already treats that line as the definition of
// "applied", so Wait reuses it. There is no separate progress record
// (design doc §1's Progress is not produced yet; see State in vm.go), so
// Wait only ever answers "not yet" or "done".
//
// A VM with no recipes can never produce a "done" line, so that case is
// refused up front, like waitReachable's not-running check. A failed run
// ("FAILED: ..." as the final line) is not treated the same way: Provision
// truncates the log again on retry, and a caller may be about to retry the
// apply that just failed. That case is left to ctx.
func waitApplied(ctx context.Context, v *config.VM) error {
	if len(v.Recipes) == 0 {
		return fmt.Errorf("%w: %s: no recipes configured", ErrCannotReach, v.Name)
	}
	return pollUntil(ctx, func() bool { return lastProvisionLineIs(v, "done") })
}

// lastProvisionLineIs reports whether the last non-blank line of v's apply
// log equals want. A missing file (never applied) reads as false, same as
// core.Logs' treatment of a missing log as an empty one.
func lastProvisionLineIs(v *config.VM, want string) bool {
	b, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil || len(b) == 0 {
		return false
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l == want
		}
	}
	return false
}

// waitStopped blocks until qemu.Running(v) turns false. Unlike Reachable and
// Applied, there is no impossible-by-construction case to refuse up front:
// a running VM can always, in principle, be stopped by something else before
// ctx gives up.
func waitStopped(ctx context.Context, v *config.VM) error {
	return pollUntil(ctx, func() bool { return !qemu.Running(v) })
}

// pollUntil calls check immediately, so an already-satisfied condition
// returns with no delay and no ticker setup. It then polls every
// pollInterval until check reports true or ctx is done.
//
// select checks ctx on every iteration, including the first wait, so a
// cancelled or expired ctx is noticed within one select rather than after a
// full pollInterval. This is what makes cancellation prompt, not just
// bounded.
func pollUntil(ctx context.Context, check func() bool) error {
	if check() {
		return nil
	}
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if check() {
				return nil
			}
		}
	}
}
