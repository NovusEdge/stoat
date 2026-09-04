package qemu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// A qemu binary that is not on PATH is the first thing Preflight checks, so
// an empty PATH reaches ErrBinaryMissing before the /dev/kvm open.
func TestPreflightReportsAMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Preflight(); !errors.Is(err, ErrBinaryMissing) {
		t.Errorf("Preflight() = %v, want ErrBinaryMissing", err)
	}
}

// A share that names a file, not a directory, is a vm.toml mistake a caller
// corrects by editing the field, so it is told apart from a missing path.
func TestPrepareSharesReportsAShareThatIsAFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STOAT_HOME", dir)
	file := filepath.Join(dir, "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Dir: filepath.Join(dir, "work"), Share: file}
	if err := prepareShares(v); !errors.Is(err, ErrShareInvalid) {
		t.Errorf("prepareShares() = %v, want ErrShareInvalid", err)
	}
}

// A stopped VM has no QMP socket to dial, and a caller that gets
// ErrMonitorUnreachable knows to start the VM rather than to retry.
func TestDialQMPReportsAnUnreachableMonitor(t *testing.T) {
	v := &config.VM{Name: "work", Dir: t.TempDir()}
	if _, err := dialQMP(v); !errors.Is(err, ErrMonitorUnreachable) {
		t.Errorf("dialQMP() = %v, want ErrMonitorUnreachable", err)
	}
}

// TypeConsolePassword refuses a VM with no password rather than typing
// nothing at the login prompt and reporting success.
func TestTypeConsolePasswordReportsNoPassword(t *testing.T) {
	v := &config.VM{Name: "work", Dir: t.TempDir()}
	if err := TypeConsolePassword(v); !errors.Is(err, ErrNoConsolePassword) {
		t.Errorf("TypeConsolePassword() = %v, want ErrNoConsolePassword", err)
	}
}
