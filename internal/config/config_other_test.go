//go:build !linux

package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/hostops"
)

func TestUnsupportedConfigOperationsNoMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stoat")
	t.Setenv("STOAT_HOME", root)

	if err := EnsureRoot(); !errors.Is(err, hostops.ErrUnsupported) {
		t.Fatalf("EnsureRoot() = %v, want ErrUnsupported", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("EnsureRoot() created %q on an unsupported host; stat err = %v", root, err)
	}

	vmDir := filepath.Join(root, "work")
	v := &VM{Name: "work", Dir: vmDir, Mode: "live", SSHPort: 2200}
	if err := v.Save(); !errors.Is(err, hostops.ErrUnsupported) {
		t.Fatalf("VM.Save() = %v, want ErrUnsupported", err)
	}
	if _, err := os.Stat(vmDir); !os.IsNotExist(err) {
		t.Fatalf("VM.Save() created %q on an unsupported host; stat err = %v", vmDir, err)
	}

	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "shared", "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "vm.toml"), []byte("marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete(); !errors.Is(err, hostops.ErrUnsupported) {
		t.Fatalf("VM.Delete() = %v, want ErrUnsupported", err)
	}
	for _, path := range []string{vmDir, filepath.Join(root, "shared", "work")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("VM.Delete() changed %q on an unsupported host: %v", path, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(vmDir, "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "marker" {
		t.Errorf("VM.Delete() rewrote vm.toml to %q", got)
	}
}

func TestHostConfigLoadReadsStoppedMetadataWithoutMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stoat")
	t.Setenv("STOAT_HOME", root)
	vmDir := filepath.Join(root, "stopped")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vmTOML := []byte("name = \"stopped\"\nmode = \"live\"\nos = \"alpine\"\nsshport = 2200\n")
	if err := os.WriteFile(filepath.Join(vmDir, "vm.toml"), vmTOML, 0o644); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(vmDir, "qemu.pid")
	if err := os.WriteFile(pidPath, []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := Load("stopped")
	if err != nil {
		t.Fatalf("Load(stopped): %v", err)
	}
	if v.Name != "stopped" || v.Mode != "live" || v.SSHPort != 2200 {
		t.Fatalf("Load(stopped) = %+v, want stored metadata", v)
	}
	gotTOML, err := os.ReadFile(filepath.Join(vmDir, "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotTOML) != string(vmTOML) {
		t.Errorf("Load(stopped) rewrote vm.toml: got %q, want %q", gotTOML, vmTOML)
	}
	gotPID, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotPID) != "999999" {
		t.Errorf("Load(stopped) removed or rewrote stale pidfile: got %q", gotPID)
	}
}
