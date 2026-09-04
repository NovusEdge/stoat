package core

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// fakeSSHD listens on 127.0.0.1, on port, or a free one if port is 0. It
// answers every connection with a real SSH identification banner, so
// sshBannerUp's "SSH-" check passes without a real sshd installed. It
// returns the bound port and a func the caller must defer to shut it down.
func fakeSSHD(t *testing.T, port int) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = c.Write([]byte("SSH-2.0-fake\r\n"))
				<-done
				_ = c.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() {
		close(done)
		_ = l.Close()
	}
}

func TestWaitUnknownVM(t *testing.T) {
	root(t)
	err := Wait(context.Background(), "nope", UntilStopped)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestWaitStoppedAlreadyStoppedReturnsImmediately(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2300}).Save(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilStopped); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already stopped)", elapsed)
	}
}

func TestWaitReachableAlreadyUpReturnsImmediately(t *testing.T) {
	root(t)
	port, stop := fakeSSHD(t, 0)
	defer stop()
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: port}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilReachable); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already reachable)", elapsed)
	}
}

func TestWaitReachablePollsUntilUp(t *testing.T) {
	root(t)
	// Reserve a port, save the VM against it, then only start the fake sshd
	// after a delay; Wait must poll rather than answering on its first
	// check.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: port}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	go func() {
		time.Sleep(2 * pollInterval)
		_, stop := fakeSSHD(t, port)
		defer stop()
		time.Sleep(2 * time.Second)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilReachable); err != nil {
		t.Fatalf("Wait did not observe the delayed sshd: %v", err)
	}
}

func TestWaitReachableNotRunningReturnsErrCannotReachImmediately(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2301}).Save(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := Wait(context.Background(), "work", UntilReachable)
	if !errors.Is(err, ErrCannotReach) {
		t.Fatalf("err = %v, want ErrCannotReach", err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate refusal rather than a block", elapsed)
	}
}

func TestWaitAppliedNoRecipesReturnsErrCannotReachImmediately(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2302}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	start := time.Now()
	err := Wait(context.Background(), "work", UntilApplied)
	if !errors.Is(err, ErrCannotReach) {
		t.Fatalf("err = %v, want ErrCannotReach", err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate refusal rather than a block", elapsed)
	}
}

func TestWaitAppliedAlreadyDoneReturnsImmediately(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2303, Recipes: []string{"devtools.alpine.sh"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := writeProvisionLog(v, "waiting for ssh...\n=== recipe devtools.alpine.sh ===\ndone\n"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if err := Wait(context.Background(), "work", UntilApplied); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want near-instant (already applied)", elapsed)
	}
}

func TestWaitAppliedPollsUntilDoneAppears(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2304, Recipes: []string{"devtools.alpine.sh"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := writeProvisionLog(v, "waiting for ssh...\n"); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(2 * pollInterval)
		_ = writeProvisionLog(v, "waiting for ssh...\ndone\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilApplied); err != nil {
		t.Fatalf("Wait did not observe the log turning to done: %v", err)
	}
}

func writeProvisionLog(v *config.VM, content string) error {
	return os.WriteFile(v.ProvisionLogPath(), []byte(content), 0o644)
}

func TestWaitStoppedPollsUntilProcessExits(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2305}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	stop := fakeRunning(t, v)

	go func() {
		time.Sleep(2 * pollInterval)
		stop()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Wait(ctx, "work", UntilStopped); err != nil {
		t.Fatalf("Wait did not observe the process exiting: %v", err)
	}
}

// TestWaitCtxCancellationIsPrompt must fail if the ctx.Done() check in
// pollUntil is removed. A VM that is running but never becomes reachable
// would otherwise poll forever. Cancelling ctx right away must return well
// under the test's own timeout, a tripwire for "this would have hung".
func TestWaitCtxCancellationIsPrompt(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2306}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	// No sshd anywhere near this port; UntilReachable can never succeed on
	// its own, so a non-prompt implementation would run until it either
	// busy-loops or hits the ctx deadline far later than cancellation.

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Wait(ctx, "work", UntilReachable)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %s to notice cancellation, want well under a second", elapsed)
	}
}

func TestWaitCtxDeadlineExceeded(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2307}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := Wait(ctx, "work", UntilReachable)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %s past a 200ms deadline, want well under a second past it", elapsed)
	}
}
