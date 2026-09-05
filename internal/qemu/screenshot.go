package qemu

import (
	"fmt"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
)

// Screenshot writes the guest's framebuffer to path as a PNG.
//
// QMP screendump takes format: "png" from QEMU 7.1 on, verified by hand
// against QEMU 11.1, so nothing here converts a PPM afterwards. The
// framebuffer exists whatever the display backend is, so this answers for a
// GTK window and for a VNC socket alike.
//
// qemu writes the file itself, as its own user, so path must be absolute:
// a relative one resolves against qemu's working directory, not the
// caller's.
func Screenshot(v *config.VM, path string) error {
	if !Running(v) {
		return fmt.Errorf("%w: %s is not running", ErrNotRunning, v.Name)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: screenshot path %s is not absolute", ErrScreenshotFailed, path)
	}
	q, err := dialQMP(v)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	if _, err := q.command("screendump", map[string]any{"filename": path, "format": "png"}); err != nil {
		return fmt.Errorf("%w: %v", ErrScreenshotFailed, err)
	}
	return nil
}
