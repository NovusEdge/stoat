//go:build !linux

package qemu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/hostops"
)

func TestUnsupportedQEMUOperationsNoMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stoat")
	t.Setenv("STOAT_HOME", root)
	vmDir := filepath.Join(root, "work")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "cloud", Backend: "cloudinit", Dir: vmDir}
	if err := os.WriteFile(v.PidPath(), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.MonitorPath(), []byte("pre-existing monitor marker"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, operation := range []struct {
		name string
		call func() error
	}{
		{name: "Start", call: func() error { return Start(v) }},
		{name: "Stop", call: func() error { return Stop(v) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			if !errors.Is(err, hostops.ErrUnsupported) {
				t.Fatalf("qemu.%s() = %v, want ErrUnsupported", operation.name, err)
			}
			for _, path := range []string{v.PidPath(), v.MonitorPath()} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("qemu.%s() changed %q before refusing: %v", operation.name, path, err)
				}
			}
			if _, err := os.Stat(v.WorkDir()); !os.IsNotExist(err) {
				t.Errorf("qemu.%s() created the backend/share artifact %q; stat err = %v", operation.name, v.WorkDir(), err)
			}
		})
	}
}
