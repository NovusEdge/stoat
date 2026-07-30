package sshx

import (
	"net"
	"path/filepath"
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

func TestWaitSucceedsOncePortAccepts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir()}
	if err := Wait(v, 2*time.Second); err != nil {
		t.Errorf("Wait failed against an open port: %v", err)
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
	_ = filepath.Join
}
