package qemu

import "github.com/novusedge/stoat/internal/config"

// Screenshot writes v's framebuffer to path as a PNG.
//
// Stub: TestScreenshotRefusesAStoppedVM pins the ErrNotRunning contract;
// task 5 fills in the QMP screendump call.
func Screenshot(v *config.VM, path string) error {
	return nil
}
