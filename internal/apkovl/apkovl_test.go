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
	// including modloop — the guest boots with no kernel modules.
	if _, ok := content["etc/.default_boot_services"]; !ok {
		t.Error("missing etc/.default_boot_services — guest would boot with no modules")
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
		t.Error("guest host key not baked in — ssh would report a changed host key each boot")
	}
}

func TestBuildOmitsFstabWhenNoShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "bare", Mode: "live", Dir: filepath.Join(root, "bare")}
	if err := Build(v); err != nil {
		t.Fatal(err)
	}
	content, _ := entries(t, filepath.Join(v.OvlDir(), "stoat.apkovl.tar.gz"))
	if strings.Contains(content["etc/fstab"], "/mnt/host") {
		t.Error("fstab mounts /mnt/host for a VM with no share")
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
