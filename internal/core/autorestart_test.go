package core

import (
	"context"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

func TestAutoRestartAfterInstallSkipsNonDiskVM(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2400}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	start := time.Now()
	restarted, err := AutoRestartAfterInstall(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Errorf("restarted = true, want false: not a disk VM")
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate no-op", elapsed)
	}
}

func TestAutoRestartAfterInstallSkipsInstalledDiskVM(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "disk", Installed: true, RAM: 1024, CPUs: 1, SSHPort: 2401}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	start := time.Now()
	restarted, err := AutoRestartAfterInstall(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Errorf("restarted = true, want false: already installed")
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate no-op", elapsed)
	}
}

func TestAutoRestartAfterInstallSkipsWhenNotRunning(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "disk", Installed: false, RAM: 1024, CPUs: 1, SSHPort: 2402}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"

	start := time.Now()
	restarted, err := AutoRestartAfterInstall(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if restarted {
		t.Errorf("restarted = true, want false: qemu is not running")
	}
	if elapsed := time.Since(start); elapsed > pollInterval {
		t.Errorf("took %s, want an immediate no-op", elapsed)
	}
}

// TestAutoRestartAfterInstallGivesUpSilentlyOnTimeout pins the awaitSSH-style
// contract: an installer that never stops must not surface an error, only
// give up once ctx runs out.
func TestAutoRestartAfterInstallGivesUpSilentlyOnTimeout(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "disk", Installed: false, RAM: 1024, CPUs: 1, SSHPort: 2403}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	restarted, err := AutoRestartAfterInstall(ctx, "work")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err = %v, want nil (silent give-up)", err)
	}
	if restarted {
		t.Errorf("restarted = true, want false: the installer never stopped")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %s past a 300ms ctx, want well under a second past it", elapsed)
	}
}

// TestAutoRestartAfterInstallAttemptsStartOnceInstallerStops proves the
// installer-stop signal actually drives a restart attempt, not just the
// guard. qemu.Start has no fake seam, so the restart attempt fails here for
// lack of a real install ISO (v.ISOPath() names nothing on disk); the test
// asserts on that failure's shape rather than a real boot, distinguishing a
// restart ATTEMPT (the failure names v's ISO path) from the guard's silent
// no-op (nil error, near-instant return).
func TestAutoRestartAfterInstallAttemptsStartOnceInstallerStops(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "disk", Installed: false, OS: "alpine", Backend: "apkovl", RAM: 1024, CPUs: 1, SSHPort: 2404}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)

	go func() {
		time.Sleep(2 * pollInterval)
		stop()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	restarted, err := AutoRestartAfterInstall(ctx, "work")
	elapsed := time.Since(start)

	if elapsed < 2*pollInterval {
		t.Errorf("took %s, want at least %s: it must wait for the installer to stop", elapsed, 2*pollInterval)
	}
	if restarted {
		t.Errorf("restarted = true, want false: the real Start attempt has no ISO to boot")
	}
	if err == nil {
		t.Fatal("err = nil, want a real error proving Start was attempted")
	}
}
