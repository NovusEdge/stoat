//go:build !linux

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/hostops"
)

func TestUnsupportedBareTUIStartupNoMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "stoat")
	t.Setenv("STOAT_HOME", root)

	err := Run()
	if !errors.Is(err, hostops.ErrUnsupported) {
		t.Fatalf("Run() = %v, want ErrUnsupported before TUI setup", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("Run() created STOAT_HOME %q before refusing; stat err = %v", root, err)
	}
}
