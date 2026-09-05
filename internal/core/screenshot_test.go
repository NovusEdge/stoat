package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// Polling a boot is the use this command exists for, and such a caller takes
// several frames a second. The name resolves to the second, so two frames in
// one second must not become one file. shotPath takes the time as a parameter
// because the collision cannot be reached through Screenshot without a
// running VM and a clock the test controls.
func TestScreenshotNamesDoNotCollideWithinASecond(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 5, 14, 3, 2, 0, time.UTC)

	for _, want := range []string{
		"2026-09-05T140302Z.png",
		"2026-09-05T140302Z-2.png",
		"2026-09-05T140302Z-3.png",
	} {
		got := shotPath(dir, at)
		if filepath.Base(got) != want {
			t.Fatalf("shotPath() = %q, want %q", filepath.Base(got), want)
		}
		if err := os.WriteFile(got, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
