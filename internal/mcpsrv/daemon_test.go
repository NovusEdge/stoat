package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestMain doubles as the server `mcp up` launches: the daemon test points
// executable() at this binary, so the child must serve HTTP instead of running
// the suite again.
func TestMain(m *testing.M) {
	if addr := os.Getenv("STOAT_MCP_TEST_CHILD"); addr != "" {
		if err := ServeHTTP(context.Background(), addr, Options{Version: "test"}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// childExe writes a launcher that starts this test binary in server mode. Up
// passes "mcp serve --http <addr>", which the child ignores in favour of the
// address in its environment.
func childExe(t *testing.T, addr string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the shell launcher this test writes is POSIX-only")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "child.sh")
	script := "#!/bin/sh\nexec env STOAT_MCP_TEST_CHILD=" + addr + " " + self + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestUpStatusDown(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	addr := freeAddr(t)
	exe := childExe(t, addr)
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = os.Executable })

	dir := t.TempDir()
	d, err := Up(dir, addr)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() { _, _ = Down(dir) })
	if d.Addr != addr || d.PID == 0 {
		t.Fatalf("Up returned %+v", d)
	}

	got, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 1 || got[0].Addr != addr {
		t.Fatalf("Status returned %+v", got)
	}

	if _, err := Down(dir); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := os.Stat(statePath(d.Dir)); !os.IsNotExist(err) {
		t.Fatalf("Down left the record behind: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for alive(d.PID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(d.PID) {
		t.Fatalf("pid %d survived Down", d.PID)
	}
}

func TestUpRefusesSecondServerForOneProject(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	addr := freeAddr(t)
	exe := childExe(t, addr)
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = os.Executable })

	dir := t.TempDir()
	if _, err := Up(dir, addr); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() { _, _ = Down(dir) })

	if _, err := Up(dir, freeAddr(t)); err == nil {
		t.Fatal("Up started a second server for the same project")
	}
}

// A second project pointed at an address another server already holds must
// fail. Dialling the address alone would find the first project's server
// answering and record a daemon this call never started.
func TestUpRefusesAnAddressAlreadyServed(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	addr := freeAddr(t)
	exe := childExe(t, addr)
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = os.Executable })

	first := t.TempDir()
	if _, err := Up(first, addr); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() { _, _ = Down(first) })

	second := t.TempDir()
	if _, err := Up(second, addr); err == nil {
		t.Fatal("Up accepted an address another server holds")
	}
	if _, err := os.Stat(statePath(second)); !os.IsNotExist(err) {
		t.Error("Up recorded a daemon it never started")
	}
}

func TestUpRejectsNonLoopback(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if _, err := Up(t.TempDir(), "0.0.0.0:7777"); err == nil {
		t.Fatal("Up accepted a non-loopback address")
	}
}

func TestDownWithoutServer(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if _, err := Down(t.TempDir()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Down returned %v, want ErrNotRunning", err)
	}
}

func TestStatusPrunesDeadRecord(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pid 1 is alive and answers nothing, so this record fails the address
	// half of the liveness check.
	rec := Daemon{Dir: dir, Addr: freeAddr(t), PID: 1, Started: time.Now().UTC()}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Status kept a dead record: %+v", got)
	}
	if _, err := os.Stat(statePath(dir)); !os.IsNotExist(err) {
		t.Fatal("Status left the dead record on disk")
	}
}

func TestStatusIgnoresMissingStateDir(t *testing.T) {
	t.Setenv("STOAT_HOME", filepath.Join(t.TempDir(), "absent"))
	got, err := Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got != nil {
		t.Fatalf("Status returned %+v for a missing state dir", got)
	}
}
