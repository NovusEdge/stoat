package qemu

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// A stopped VM has no framebuffer, and its dead monitor socket would
// otherwise report the transport rather than the reason.
func TestScreenshotRefusesAStoppedVM(t *testing.T) {
	dir := t.TempDir()
	v := &config.VM{Name: "work", Dir: dir}
	err := Screenshot(v, filepath.Join(dir, "shot.png"))
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("Screenshot() = %v, want ErrNotRunning", err)
	}
}
