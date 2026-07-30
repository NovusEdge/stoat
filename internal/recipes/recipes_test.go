package recipes

import (
	"os"
	"strings"
	"testing"
)

func TestInstallCopiesBundledRecipesAndPreservesEdits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	if err := Install(); err != nil {
		t.Fatal(err)
	}
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range names {
		if n == "xfce" {
			found = true
		}
		if strings.HasSuffix(n, ".sh") {
			t.Errorf("List must return names without .sh, got %q", n)
		}
	}
	if !found {
		t.Fatalf("xfce recipe not installed, got %v", names)
	}

	// A user edit must survive a later Install (i.e. an upgrade).
	edited := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(Path("xfce"), []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	got, err := Read("xfce")
	if err != nil {
		t.Fatal(err)
	}
	if got != edited {
		t.Error("Install overwrote a user-edited recipe")
	}
}

func TestInstalledRecipeConfiguresReposAndMatchesVirtioGPU(t *testing.T) {
	// The recipe runs after boot over ssh, so it may install from the
	// network — but it must set up repositories first. Assert through the
	// real path (Install then Read) so this proves what stoat actually
	// installs for a user, not just what's in the source tree.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("xfce")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "setup-apkrepos") {
		t.Error("xfce.sh installs packages without configuring repositories first")
	}
	// The VM boots with -vga virtio (virtio-gpu), which setup-xorg-base's
	// mesa-dri-gallium + modesetting driver already cover. xf86-video-qxl
	// is the driver for QEMU's separate QXL/SPICE adapter (-vga qxl) and
	// does not match this device.
	if strings.Contains(s, "xf86-video-qxl") {
		t.Error("xfce.sh installs xf86-video-qxl, which does not match -vga virtio (virtio-gpu)")
	}
}
