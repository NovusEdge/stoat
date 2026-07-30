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

func TestInstalledRecipeConfiguresAutologinAndGuardedStartx(t *testing.T) {
	// Provisioning must land the user in a desktop, not a bare console: tty1
	// needs to autologin root, and .profile needs to start X. Assert through
	// the real path (Install then Read) so this proves what stoat actually
	// ships, not just what's in the source tree.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("xfce")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(s, "/etc/inittab") {
		t.Error("xfce.sh does not touch /etc/inittab to configure tty1 autologin")
	}
	// busybox getty has no -a/autologin flag; the recipe must not use it.
	if strings.Contains(s, "getty -a") {
		t.Error("xfce.sh uses `getty -a`, which busybox getty does not support")
	}

	// A recipe that stacks another `exec startx` block on every re-run is
	// the obvious regression, so the guard string must be present.
	if !strings.Contains(s, "exec startx") {
		t.Error("xfce.sh does not append a startx block to /root/.profile")
	}
	if !strings.Contains(s, "grep -q 'exec startx' /root/.profile") {
		t.Error("xfce.sh does not guard the .profile append against re-running the recipe")
	}
}
