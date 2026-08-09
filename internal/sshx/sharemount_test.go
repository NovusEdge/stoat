package sshx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/apkovl"
	"github.com/novusedge/stoat/internal/config"
)

func TestShareMountTagsDiskWithShare(t *testing.T) {
	v := &config.VM{Mode: "disk", Share: "/home/u/vms"}
	tags := shareMountTags(v)
	if len(tags) != 2 {
		t.Fatalf("tags = %v, want host and work", tags)
	}
	if tags[0].Tag != "host" || tags[1].Tag != "work" {
		t.Errorf("tags = %v, want [host work]", tags)
	}
}

func TestShareMountTagsSkipsLiveVM(t *testing.T) {
	v := &config.VM{Mode: "live", Share: "/home/u/vms"}
	if tags := shareMountTags(v); tags != nil {
		t.Errorf("live VM: tags = %v, want nil", tags)
	}
}

func TestShareMountTagsSkipsCloudVM(t *testing.T) {
	v := &config.VM{Mode: "cloud", Share: "/home/u/vms"}
	if tags := shareMountTags(v); tags != nil {
		t.Errorf("cloud VM: tags = %v, want nil", tags)
	}
}

func TestShareMountTagsSkipsDiskWithNoShare(t *testing.T) {
	v := &config.VM{Mode: "disk"}
	if tags := shareMountTags(v); tags != nil {
		t.Errorf("disk VM with no share: tags = %v, want nil", tags)
	}
}

// runFstabEnsure executes fstabEnsureLine's sh fragment against a real
// temp file, proving the idempotency logic (not just its string shape).
func runFstabEnsure(t *testing.T, m apkovl.Mount9p, fstabPath string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", fstabEnsureLine(m, fstabPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fstabEnsureLine script failed: %v\n%s", err, out)
	}
}

func TestFstabEnsureLineAppendsWhenAbsent(t *testing.T) {
	fstab := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(fstab, []byte("/dev/cdrom /media/cdrom iso9660 noauto,ro 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runFstabEnsure(t, apkovl.HostMount9p, fstab)

	got, err := os.ReadFile(fstab)
	if err != nil {
		t.Fatal(err)
	}
	want := "host /mnt/host 9p trans=virtio,version=9p2000.L,ro,_netdev,nofail 0 0"
	if !strings.Contains(string(got), want) {
		t.Errorf("fstab = %q, missing %q", got, want)
	}
}

func TestFstabEnsureLineNoopsWhenPresent(t *testing.T) {
	fstab := filepath.Join(t.TempDir(), "fstab")
	initial := "work /mnt/work 9p trans=virtio,version=9p2000.L,rw,_netdev,nofail 0 0\n"
	if err := os.WriteFile(fstab, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	runFstabEnsure(t, apkovl.WorkMount9p, fstab)

	got, err := os.ReadFile(fstab)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != initial {
		t.Errorf("fstab changed when the line already existed:\ngot:  %q\nwant: %q", got, initial)
	}
}

func TestFstabEnsureLineRunTwiceAppendsOnce(t *testing.T) {
	fstab := filepath.Join(t.TempDir(), "fstab")
	if err := os.WriteFile(fstab, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	runFstabEnsure(t, apkovl.HostMount9p, fstab)
	runFstabEnsure(t, apkovl.HostMount9p, fstab)

	got, err := os.ReadFile(fstab)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "host /mnt/host"); n != 1 {
		t.Errorf("host line appears %d times after running twice, want 1:\n%s", n, got)
	}
}

func TestShareMountScriptCoversEveryTag(t *testing.T) {
	tags := []apkovl.Mount9p{apkovl.HostMount9p, apkovl.WorkMount9p}
	script := shareMountScript(tags, guestFstabPath)
	for _, m := range tags {
		if !strings.Contains(script, m.Dir) {
			t.Errorf("script missing mountpoint %q:\n%s", m.Dir, script)
		}
		if !strings.Contains(script, "mount "+shQuote(m.Dir)) {
			t.Errorf("script missing mount call for %q:\n%s", m.Dir, script)
		}
	}
}

func TestShareMountScriptEmptyForNoTags(t *testing.T) {
	if got := shareMountScript(nil, guestFstabPath); got != "" {
		t.Errorf("shareMountScript(nil, ...) = %q, want empty", got)
	}
}

// installFakeSSHEcho puts a stand-in "ssh" on PATH that runs its stdin
// through the real sh and exits, standing in for a real guest connection.
func installFakeSSHEcho(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nexec sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestProvisionMountsSharesForDiskVMWithShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	installFakeSSHEcho(t)
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir(), Mode: "disk", Share: "/home/u/vms"}
	if err := Provision(context.Background(), v); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "mounting 9p shares") {
		t.Errorf("provision log has no share-mount step:\n%s", log)
	}
}

func TestProvisionSkipsShareMountForLiveVM(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}
	installFakeSSHEcho(t)
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")

	v := &config.VM{Name: "x", SSHPort: port, Dir: t.TempDir(), Mode: "live", Share: "/home/u/vms"}
	if err := Provision(context.Background(), v); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "mounting 9p shares") {
		t.Errorf("provision log ran the share-mount step for a live VM:\n%s", log)
	}
}
