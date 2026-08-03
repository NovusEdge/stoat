package sshx

import (
	"net"
	"strings"
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
