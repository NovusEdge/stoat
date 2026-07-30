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

func TestArgsExtraGoesAfterTarget(t *testing.T) {
	t.Setenv("STOAT_HOME", "/data")
	v := &config.VM{Name: "x", SSHPort: 2201, Dir: "/data/x"}
	got := Args(v, "sh", "-s")
	if got[len(got)-2] != "sh" || got[len(got)-1] != "-s" {
		t.Errorf("extra args must come last, got %v", got)
	}
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
