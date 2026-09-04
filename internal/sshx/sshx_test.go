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
// exists to prevent. scp takes -P (capital) for the port, not ssh's -p.
// Copy-pasting Args' argv into an scp invocation would fail outright, or
// worse, silently hit -p's other meaning for scp, "preserve file times".
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
// core/copy.go. Unlike Exec, which sends its command through the guest's
// shell and must quote for it, scp never involves a remote shell at all: it
// uses the SFTP protocol by default since OpenSSH 9.0 (see `man scp`'s
// CAVEATS section; quoting is a concern only for the legacy -O protocol). A
// remote path is a literal string handed to the SFTP subsystem, so a space
// in it must survive unescaped. Wrapping it in quotes would create a file
// literally named with quote characters in it.
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

// installArgvRecordingSSH puts a stand-in "ssh" on PATH ahead of the real
// one. It appends its own argv, one invocation per line, to argvFile and
// exits 0 without reading stdin. Multiple recipe/bootstrap ssh calls append
// to the same file, so a test can assert the order and shape of each call
// Provision made.
func installArgvRecordingSSH(t *testing.T, argvFile string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> " + shellQuoteForTest(argvFile) + "\ncat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// TestProvisionRunsPythonRecipeUnderPython3 pins the transport change: a
// recipe whose manifest declares runtime = "python3" must run under
// `python3 -` over ssh, not `sh -s`, and stoat must bootstrap python3 first.
// Falsified by a Provision that keeps hardcoding "sh", "-s" regardless of
// the recipe's declared runtime.
func TestProvisionRunsPythonRecipeUnderPython3(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	rd := filepath.Join(root, "recipes", "pyrecipe")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"pyrecipe\"\nscript = \"install.py\"\nruntime = \"python3\"\n"
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vmDir := t.TempDir()
	argvFile := filepath.Join(vmDir, "ssh.argv")
	installArgvRecordingSSH(t, argvFile)

	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")
	v := &config.VM{Name: "x", SSHPort: port, Dir: vmDir, OS: "alpine", Recipes: []string{"pyrecipe"}}

	if err := Provision(context.Background(), v); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading recorded argv: %v", err)
	}
	// The bootstrap step legitimately runs under sh -s (it installs
	// python3), so only the last ssh call, the recipe body itself, is
	// checked for the runtime switch.
	lines := strings.Split(strings.TrimRight(string(argv), "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "python3 -") {
		t.Errorf("recipe body ssh call = %q, want it to end in python3 -", last)
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
// well before the guest's sshd is reachable through it.
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
	err := Wait(context.Background(), v, 500*time.Millisecond)
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
	err := Wait(context.Background(), v, 2*time.Second)
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
	// A guest sshd forking under load on a 1-vCPU VM mid-boot can take
	// longer than a couple hundred milliseconds to emit its banner. This
	// must succeed now that the per-attempt read deadline is ~2s. With the
	// old 300ms deadline, every retry's fresh connection hit the same wall
	// and this timed out no matter the overall budget.
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
	err = Wait(context.Background(), v, 3*time.Second)
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
	err := Wait(context.Background(), v, 300*time.Millisecond)
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

// acceptAndClose starts a listener that accepts and immediately closes every
// connection, with no banner. Unlike acceptOnly, which leaves the connection
// open to model libslirp's early accept, this makes both the dial and
// bannerReady's read return almost instantly on every attempt. A caller
// testing Wait's retry loop then lands reliably inside the 500ms sleep
// between attempts, instead of racing the dial or the banner read.
func acceptAndClose(t *testing.T) int {
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
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// TestWaitCancelDuringRetrySleepReturnsPromptly proves ctx is raced against
// the between-attempt sleep, not just checked once per loop iteration.
// Falsified by reverting Wait's select to a bare time.Sleep(sleep): that
// version takes close to the full 500ms to notice cancellation, failing
// this test's margin.
func TestWaitCancelDuringRetrySleepReturnsPromptly(t *testing.T) {
	port := acceptAndClose(t)
	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir()}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := Wait(ctx, v, 10*time.Second)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The retry sleep is 500ms. A version that only checks ctx between full
	// loop iterations would take close to that after cancel fires. A margin
	// well under it proves the sleep itself is interrupted, not just noticed
	// on the next iteration.
	if elapsed > 300*time.Millisecond {
		t.Errorf("Wait took %s to notice cancellation during the retry sleep, want well under 500ms", elapsed)
	}
}

// TestWaitAlreadyCancelledReturnsImmediately covers the ctx-before-any-dial
// case: nothing should be attempted at all.
func TestWaitAlreadyCancelledReturnsImmediately(t *testing.T) {
	v := &config.VM{Name: "x", SSHPort: 1, Dir: t.TempDir()} // nothing listens
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := Wait(ctx, v, 10*time.Second)
	elapsed := time.Since(start)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait took %s against an already-cancelled ctx, want near-instant", elapsed)
	}
}

// TestProvisionCancelDuringWaitReturnsPromptly covers the other half of
// cancellation: a ctx already cancelled before sshd ever answers must not
// leave Provision blocked in Wait for up to WaitTimeout. Here the port has
// nothing listening, so Wait would otherwise run the full timeout.
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
// records its own pid to pidFile, then execs into "sleep 30", replacing its
// own process image rather than forking a child. The recorded pid is
// therefore the pid Provision's cmd.Process actually signals, exactly as
// for a real ssh process. The rest of PATH is kept so the script's own
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

// processAlive reports whether pid still exists, using signal 0: the
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
// cancellation test. It asserts not just that Provision unblocks, but that
// the ssh process it started is actually gone afterwards. Falsified by
// reverting Provision's exec.CommandContext to exec.Command, which leaves
// this test hanging past its own deadline, waiting for a process cancel
// never touches.
func TestProvisionCancelKillsTheSSHProcess(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	rd := filepath.Join(root, "recipes", "long")
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "recipe.toml"), []byte("name = \"long\"\nscript = \"install.sh\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rd, "install.sh"), []byte("sleep 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	vmDir := t.TempDir()
	pidFile := filepath.Join(vmDir, "ssh.pid")
	installFakeSSH(t, pidFile)

	// A listener that answers the SSH banner immediately stands in for
	// sshd, so Wait clears at once and Provision moves on to the recipe.
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")

	v := &config.VM{Name: "x", SSHPort: port, Dir: vmDir, OS: "alpine", Recipes: []string{"long"}}

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
	// The kill is not instantaneous: cmd.Cancel sends SIGTERM, with
	// WaitDelay as the SIGKILL backstop. Give it a moment before declaring
	// the process still alive.
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

// escalate must use the guest's own escalate argv, not a hardcoded "sudo":
// a BYO image with a different sudo path, or a doas-only image, needs its
// own prefix. Root gets no prefix regardless of the guest.
func TestEscalateUsesGuestArgv(t *testing.T) {
	v := &config.VM{OS: "ubuntu", SSHUser: "stoat"}
	got := escalate(v, []string{"sh", "-s"})
	want := []string{"sudo", "-n", "sh", "-s"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v want %v", got, want)
	}
	if got := escalate(&config.VM{OS: "alpine"}, []string{"sh", "-s"}); len(got) != 2 {
		t.Errorf("root must get no prefix: %v", got)
	}
}

// A guest stoat does not know renders no prelude: the recipe runs bare, as
// it always did before this feature existed.
func TestPreludeForUnknownOSIsEmpty(t *testing.T) {
	if p := preludeFor(&config.VM{OS: "plan9"}, "sh"); p != "" {
		t.Errorf("got %q", p)
	}
}
