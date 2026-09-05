package core

import (
	"errors"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// A stopped VM has no framebuffer, so Screenshot refuses up front with the
// same typed error Stop uses, rather than failing at the monitor socket.
func TestScreenshotRefusesAStoppedVM(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Screenshot("work", ""); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Screenshot() = %v, want ErrNotRunning", err)
	}
}
