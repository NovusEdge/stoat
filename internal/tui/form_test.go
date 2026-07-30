package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestBuildVMFailedDiskCreationLeavesNoTrace drives buildVM down the disk-mode
// path with an invalid qemu-img size and asserts the VM directory (and the
// vm.toml written just before the qemu-img call) does not survive the
// failure. Before the fix, v.Save() ran unconditionally before qemu-img, so
// a bad size left a phantom, unbootable VM behind.
func TestBuildVMFailedDiskCreationLeavesNoTrace(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := config.EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	v := &config.VM{
		Name: "badsize",
		Mode: "disk",
		ISO:  "isos/alpine-standard-3.20.0-x86_64.iso",
		RAM:  1024,
		CPUs: 1,
		Disk: "8Gigs", // invalid qemu-img size
	}

	err := buildVM(v)
	if err == nil {
		t.Fatal("expected buildVM to fail on an invalid disk size")
	}

	dir := filepath.Join(config.Root(), "badsize")
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("VM directory %q survived a failed disk creation: %v", dir, statErr)
	}
}
