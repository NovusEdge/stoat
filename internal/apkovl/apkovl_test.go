package apkovl

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// entries extracts the tarball into a name -> content map, plus a name -> header map.
func entries(t *testing.T, path string) (map[string]string, map[string]*tar.Header) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	content := map[string]string{}
	hdrs := map[string]*tar.Header{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		content[h.Name] = string(b)
		hdrs[h.Name] = h
	}
	return content, hdrs
}

func TestBuildProducesTheAlpineContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	v := &config.VM{
		Name: "live1", Mode: "live", RAM: 2048, CPUs: 2,
		Share: "/home/u/vms", SSHPort: 2201,
		Dir: filepath.Join(root, "live1"),
	}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz")
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("overlay not written: %v", err)
	}
	content, hdrs := entries(t, out)

	// Without this file the initramfs skips every default service,
	// including modloop, so the guest boots with no kernel modules.
	if _, ok := content["etc/.default_boot_services"]; !ok {
		t.Error("missing etc/.default_boot_services: guest would boot with no modules")
	}
	if content["etc/.default_boot_services"] != "" {
		t.Error("etc/.default_boot_services must be empty")
	}

	// sshd without networking is a daemon on an unreachable port.
	if !strings.Contains(content["etc/network/interfaces"], "eth0") {
		t.Error("etc/network/interfaces does not configure eth0")
	}
	for _, link := range []string{"etc/runlevels/boot/networking", "etc/runlevels/default/sshd"} {
		h, ok := hdrs[link]
		if !ok {
			t.Errorf("missing runlevel symlink %s", link)
			continue
		}
		if h.Typeflag != tar.TypeSymlink {
			t.Errorf("%s is type %v, want symlink", link, h.Typeflag)
		}
	}

	// Only ISO-resident packages may be listed; xfce4 is not on the ISO.
	world := content["etc/apk/world"]
	if !strings.Contains(world, "openssh") {
		t.Error("etc/apk/world missing openssh")
	}
	if strings.Contains(world, "xfce") || strings.Contains(world, "xorg") {
		t.Error("etc/apk/world names a package that is not on the ISO")
	}

	if !strings.HasPrefix(content["root/.ssh/authorized_keys"], "ssh-ed25519 ") {
		t.Error("authorized_keys does not contain stoat's public key")
	}
	if m := hdrs["root/.ssh/authorized_keys"].Mode; m != 0o600 {
		t.Errorf("authorized_keys mode = %o, want 600", m)
	}

	if !strings.Contains(content["etc/fstab"], "9p") ||
		!strings.Contains(content["etc/fstab"], "/mnt/host") {
		t.Errorf("etc/fstab missing the 9p share line: %q", content["etc/fstab"])
	}
	if !strings.Contains(content["etc/apk/repositories"], "alpinelinux.org") {
		t.Error("etc/apk/repositories does not point at a mirror")
	}
	if !strings.Contains(content["etc/ssh/ssh_host_ed25519_key"], "PRIVATE KEY") {
		t.Error("guest host key not baked in: ssh would report a changed host key each boot")
	}
}

// A VM with no configured share still mounts /mnt/work, so localmount is
// always needed now. Only /mnt/host is conditional.
func TestBuildOmitsHostMountWhenNoShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "bare", Mode: "live", Dir: filepath.Join(root, "bare")}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}
	content, hdrs := entries(t, filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz"))
	if strings.Contains(content["etc/fstab"], "/mnt/host") {
		t.Error("fstab mounts /mnt/host for a VM with no share")
	}
	if !strings.Contains(content["etc/fstab"], "/mnt/work") {
		t.Error("fstab must mount /mnt/work even with no share configured")
	}
	if _, ok := hdrs["etc/runlevels/boot/localmount"]; !ok {
		t.Error("localmount symlink missing, so /mnt/work would never mount")
	}
}

// The host mount is read-only and the work mount is not. nofail is on both:
// some guest kernels ship no 9p module, and an unmountable share must not
// hold up boot.
func TestBuildFstabMountOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "shared", Mode: "live", Share: "/home/u/vms", Dir: filepath.Join(root, "shared")}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}
	content, _ := entries(t, filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz"))
	for _, want := range []string{
		"host /mnt/host 9p trans=virtio,version=9p2000.L,ro,_netdev,nofail 0 0",
		"work /mnt/work 9p trans=virtio,version=9p2000.L,rw,_netdev,nofail 0 0",
	} {
		if !strings.Contains(content["etc/fstab"], want) {
			t.Errorf("fstab missing %q, got:\n%s", want, content["etc/fstab"])
		}
	}
}

// TestBuildSymlinksLocalmountWhenShareConfigured proves that a configured
// share is actually mounted at boot: the initramfs's default-boot-services
// set does not include localmount, and the overlay otherwise only symlinks
// networking (boot) and sshd (default), so an fstab entry with no
// corresponding localmount symlink is never processed.
func TestBuildSymlinksLocalmountWhenShareConfigured(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{
		Name: "shared", Mode: "live", Share: "/home/u/vms",
		Dir: filepath.Join(root, "shared"),
	}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}
	_, hdrs := entries(t, filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz"))
	h, ok := hdrs["etc/runlevels/boot/localmount"]
	if !ok {
		t.Fatal("missing etc/runlevels/boot/localmount symlink: share would never be mounted at boot")
	}
	if h.Typeflag != tar.TypeSymlink || h.Linkname != "/etc/init.d/localmount" {
		t.Errorf("etc/runlevels/boot/localmount = %+v, want symlink to /etc/init.d/localmount", h)
	}
}

// TestBuildTempFileDoesNotMatchApkovlGlob proves the in-progress temp file
// cannot be picked up as the overlay by Alpine's nlplug-findfs, which globs
// *.apkovl.tar.gz* (note the trailing wildcard) at the root of the exported
// directory. An orphaned temp file matching that glob would be selected as
// the overlay after a kill or power loss mid-Build, silently unpacking a
// truncated tarball.
func TestBuildTempFileDoesNotMatchApkovlGlob(t *testing.T) {
	if strings.Contains(tmpName, "apkovl.tar.gz") {
		t.Fatalf("temp file name %q matches the nlplug-findfs *.apkovl.tar.gz* glob", tmpName)
	}
}

// TestBuildFailureLeavesGoodOverlayIntact proves that a failed rebuild does
// not leave a corrupt tarball at the canonical path, and does not destroy a
// previously-good overlay. It forces the failure by making the output
// directory read-only, which os.Create refuses to write into; that only
// works as non-root, since root ignores permission bits.
func TestBuildFailureLeavesGoodOverlayIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are ignored, cannot force a write failure")
	}

	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "live1", Mode: "live", Dir: filepath.Join(root, "live1")}

	if err := Build(v); err != nil {
		t.Fatalf("initial build failed: %v", err)
	}
	out := filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz")
	before, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading good overlay: %v", err)
	}
	// Sanity check: the good overlay is actually extractable.
	entries(t, out)

	if err := os.Chmod(v.OvlDir(), 0o555); err != nil {
		t.Fatalf("chmod ovl dir: %v", err)
	}
	defer os.Chmod(v.OvlDir(), 0o755)

	if err := Build(v); err == nil {
		t.Fatal("expected Build to fail when the ovl dir is read-only")
	}

	if err := os.Chmod(v.OvlDir(), 0o755); err != nil {
		t.Fatalf("restoring ovl dir perms: %v", err)
	}

	if _, err := os.Stat(filepath.Join(v.OvlDir(), tmpName)); !os.IsNotExist(err) {
		t.Errorf("temp file left behind after failed build: err=%v", err)
	}

	after, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("original overlay missing after failed rebuild: %v", err)
	}
	if string(after) != string(before) {
		t.Error("failed rebuild altered the previously-good overlay")
	}
	// Still extractable, not truncated/corrupt.
	entries(t, out)
}

// TestBuildRemovesLegacyTmpFile proves that a file orphaned by a crash
// mid-Build from before tmpName was introduced (stoat.apkovl.tar.gz.tmp,
// which DOES match nlplug-findfs's *.apkovl.tar.gz* glob) is cleaned up by
// Build, so it can never be picked up by the initramfs as a truncated
// overlay.
func TestBuildRemovesLegacyTmpFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "live1", Mode: "live", Dir: filepath.Join(root, "live1")}

	if err := os.MkdirAll(v.OvlDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz.tmp")
	if err := os.WriteFile(legacy, []byte("truncated garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Build(v); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy temp file still present after Build: err=%v", err)
	}
	out := filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz")
	if _, err := os.Stat(out); err != nil {
		t.Errorf("real overlay missing after Build: %v", err)
	}
}

func TestBuildIsDeterministicallyRegenerated(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "x", Mode: "live", Dir: filepath.Join(root, "x")}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}
	if err := Build(v); err != nil {
		t.Fatalf("rebuild over an existing overlay failed: %v", err)
	}
}
