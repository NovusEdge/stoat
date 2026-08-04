package sshx

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

func TestArgs(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := strings.Join(Args(v), " ")

	for _, want := range []string{
		"-p 2201",
		"-o StrictHostKeyChecking=no",
		"-o UserKnownHostsFile=/dev/null",
		"-o BatchMode=yes",
		"-i /data/id_stoat",
		"root@127.0.0.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
	if strings.Contains(got, "localhost") {
		t.Error("must target 127.0.0.1 explicitly, not localhost")
	}
}

func TestArgsUsesConfiguredSSHUser(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x", SSHUser: "stoat"}
	got := strings.Join(Args(v), " ")

	if !strings.Contains(got, "stoat@127.0.0.1") {
		t.Errorf("expected stoat@127.0.0.1 in: %s", got)
	}
	if strings.Contains(got, "root@127.0.0.1") {
		t.Errorf("must not target root@127.0.0.1 when SSHUser is set, got: %s", got)
	}
}

// TestCopyArgsUsesScpsPortFlagNotSSHs pins the regression this package
// exists to prevent: scp takes -P (capital) for the port, not ssh's -p —
// copy-pasting Args' argv into an scp invocation would either fail outright
// or (worse) silently hit -p's OTHER meaning for scp, "preserve file times".
func TestCopyArgsUsesScpsPortFlagNotSSHs(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := CopyArgs(v, "/tmp/local", "/root/remote", true)

	if !containsPair(got, "-P", "2201") {
		t.Errorf("missing -P 2201 in: %v", got)
	}
	for i, a := range got {
		if a == "-p" {
			t.Errorf("argv has bare -p at %d (scp's -p means \"preserve\", not port): %v", i, got)
		}
	}
}

// TestCopyArgsSharesConnOptionsWithArgs guards against the shared options
// drifting apart from ssh's: this is the whole point of factoring
// connOptions out in the first place.
func TestCopyArgsSharesConnOptionsWithArgs(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := strings.Join(CopyArgs(v, "/tmp/local", "/root/remote", true), " ")

	for _, want := range []string{
		"-o StrictHostKeyChecking=no",
		"-o UserKnownHostsFile=/dev/null",
		"-o BatchMode=yes",
		"-i /data/id_stoat",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %s", want, got)
		}
	}
}

// TestCopyArgsDirection pins which side of scp's argv gets the remote spec:
// scp reads its LAST argument as the destination, so getting toRemote's
// branch backwards would silently swap upload and download.
func TestCopyArgsDirection(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}

	up := CopyArgs(v, "/tmp/local", "/root/remote", true)
	if up[len(up)-2] != "/tmp/local" || up[len(up)-1] != "root@127.0.0.1:/root/remote" {
		t.Errorf("CopyTo argv = %v, want local then remote", up)
	}

	down := CopyArgs(v, "/tmp/local", "/root/remote", false)
	if down[len(down)-2] != "root@127.0.0.1:/root/remote" || down[len(down)-1] != "/tmp/local" {
		t.Errorf("CopyFrom argv = %v, want remote then local", down)
	}
}

// TestCopyArgsRemotePathIsNotShellQuoted pins the decision documented in
// core/copy.go: unlike Exec, which sends its command through the GUEST'S
// shell and must quote for it, scp (SFTP protocol, the default since OpenSSH
// 9.0 — see `man scp`'s CAVEATS section, which says quoting is a concern
// only for the legacy -O protocol) never involves a remote shell at all. A
// remote path is a literal string handed to the SFTP subsystem, so a space
// in it must survive completely unescaped — wrapping it in quotes would
// create a file literally named with quote characters in it.
func TestCopyArgsRemotePathIsNotShellQuoted(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := CopyArgs(v, "/tmp/local", "/root/my file.txt", true)

	want := "root@127.0.0.1:/root/my file.txt"
	if got[len(got)-1] != want {
		t.Errorf("remote spec = %q, want %q verbatim, unquoted", got[len(got)-1], want)
	}
}

func TestArgsExtraGoesAfterTarget(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := Args(v, "sh", "-s")
	if got[len(got)-2] != "sh" || got[len(got)-1] != "-s" {
		t.Errorf("extra args must come last, got %v", got)
	}
}

func containsPair(argv []string, flag, val string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == val {
			return true
		}
	}
	return false
}

// acceptOnly starts a listener that accepts connections and, for each one,
// writes body (if any) then leaves it open. This models QEMU's user-mode
// networking: the host-side accept() happens immediately at device init,
// well before the guest's sshd is actually reachable through it.
func acceptOnly(t *testing.T, body string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			if body != "" {
				c.Write([]byte(body))
			}
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestWaitTimesOutWhenAcceptedButNoBanner(t *testing.T) {
	// A bare TCP accept with no SSH banner is exactly what libslirp does
	// before the guest's sshd is actually up. Wait must not treat this as
	// ready.
	port := acceptOnly(t, "")

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir()}
	start := time.Now()
	err := Wait(v, 500*time.Millisecond)
	elapsed := time.Since(start)
	t.Logf("accept-without-banner: Wait took %s", elapsed)
	if err == nil {
		t.Fatal("Wait succeeded against a connection with no SSH banner")
	}
	if elapsed > 3*time.Second {
		t.Error("Wait overran its timeout badly")
	}
}

func TestWaitSucceedsOnceBannerArrives(t *testing.T) {
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir()}
	start := time.Now()
	err := Wait(v, 2*time.Second)
	elapsed := time.Since(start)
	t.Logf("accept-with-banner: Wait took %s", elapsed)
	if err != nil {
		t.Errorf("Wait failed against a listener sending a real SSH banner: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Error("Wait should succeed quickly once the banner arrives")
	}
}

func TestWaitSucceedsOnSlowBanner(t *testing.T) {
	// A guest sshd forking under load on a 1-vCPU VM mid-boot can plausibly
	// take longer than a couple hundred milliseconds to emit its banner.
	// This must succeed now that the per-attempt read deadline is ~2s;
	// with the old 300ms deadline every retry's fresh connection would hit
	// the same wall and this would time out no matter the overall budget.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
		c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
	}()
	port := l.Addr().(*net.TCPAddr).Port

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir()}
	start := time.Now()
	err = Wait(v, 3*time.Second)
	elapsed := time.Since(start)
	t.Logf("slow-banner (500ms): Wait took %s", elapsed)
	if err != nil {
		t.Errorf("Wait failed against a listener whose banner arrives after 500ms: %v", err)
	}
}

func TestWaitTimesOutOnClosedPort(t *testing.T) {
	// Port 1 on loopback: reserved, nothing listens.
	v := &config.VM{Name: "x", SSHPort: 1, Dir: t.TempDir()}
	start := time.Now()
	err := Wait(v, 300*time.Millisecond)
	if err == nil {
		t.Fatal("Wait returned nil for a closed port")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("Wait overran its timeout badly")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Errorf("error should name the vm, got %v", err)
	}
}

// TestProvisionCancelDuringWaitReturnsPromptly covers the other half of
// cancellation: a ctx already cancelled before sshd ever answers must not
// leave Provision blocked in Wait for up to WaitTimeout (here, a port with
// nothing listening, so Wait would otherwise run the full timeout).
func TestProvisionCancelDuringWaitReturnsPromptly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}

	v := &config.VM{Name: "x", SSHPort: 1, Dir: t.TempDir()} // port 1: nothing listens

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := Provision(ctx, v)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Provision took %s to notice an already-cancelled ctx during Wait", elapsed)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatalf("reading provision log: %v", err)
	}
	if !strings.Contains(string(log), "CANCELLED") {
		t.Errorf("provision log does not mention cancellation:\n%s", log)
	}
}

// installFakeSSH puts a stand-in "ssh" on PATH ahead of the real one. It
// records its own pid to pidFile, then execs into "sleep 30" — replacing its
// own process image rather than forking a child — so the pid recorded is
// the pid that Provision's cmd.Process actually signals, exactly as it would
// be for a real ssh process. The rest of PATH is kept so the script's own
// "sleep" can still be resolved.
func installFakeSSH(t *testing.T, pidFile string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho $$ > " + shellQuoteForTest(pidFile) + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// waitForFile polls until path exists or timeout, so the test does not race
// the fake ssh script writing its pid before cancelling.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared within %s", path, timeout)
}

// processAlive reports whether pid still exists, using signal 0 — the
// standard liveness probe that delivers nothing but still fails against a
// dead pid.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// TestProvisionCancelKillsTheSSHProcess is the strong version of the
// cancellation test: it does not just assert Provision unblocks, it asserts
// the ssh process it started is actually gone afterwards. Falsified by
// reverting Provision's exec.CommandContext to exec.Command, which makes
// this test hang past its own deadline waiting for a process cancel never
// touches.
func TestProvisionCancelKillsTheSSHProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "recipes", "long.sh"), []byte("sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vmDir := t.TempDir()
	pidFile := filepath.Join(vmDir, "ssh.pid")
	installFakeSSH(t, pidFile)

	// A listener that answers the SSH banner immediately stands in for
	// sshd, so Wait clears at once and Provision moves on to the recipe.
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")

	v := &config.VM{Name: "x", SSHPort: port, Dir: vmDir, Recipes: []string{"long.sh"}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		waitForFile(t, pidFile, 3*time.Second)
		cancel()
	}()

	provisionErr := make(chan error, 1)
	go func() { provisionErr <- Provision(ctx, v) }()

	<-done
	select {
	case err := <-provisionErr:
		if err == nil {
			t.Error("Provision returned nil error for a cancelled run")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Provision did not return within 10s of cancellation")
	}

	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("reading pidfile: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parsing pid: %v", err)
	}
	// The kill is not necessarily instantaneous (cmd.Cancel sends SIGTERM,
	// with WaitDelay as the SIGKILL backstop), so give it a moment before
	// declaring the process still alive.
	deadline := time.Now().Add(6 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Errorf("ssh process (pid %d) is still running after Provision's ctx was cancelled", pid)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatalf("reading provision log: %v", err)
	}
	if !strings.Contains(string(log), "CANCELLED") {
		t.Errorf("provision log does not mention cancellation:\n%s", log)
	}
	if strings.Contains(string(log), "\ndone") {
		t.Errorf("provision log reports success for a cancelled run:\n%s", log)
	}
}
