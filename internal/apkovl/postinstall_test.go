package apkovl

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runFix runs one post-install fragment against a fake target root, the way
// the installer runs it, and returns nothing: a failing shell fails the test.
func runFix(t *testing.T, target, fragment string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not installed")
	}
	cmd := exec.Command("sh", "-c", "set -e\ntarget="+target+"\n"+fragment)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fragment failed: %v\n%s", err, out)
	}
}

// syslinux cancels TIMEOUT when a key arrives during the countdown and then
// waits forever. TOTALTIMEOUT fires whatever the user typed. The install runs
// the append on a disk that may already carry the line, so it must not stack.
func TestExtlinuxTimeoutFixIsIdempotent(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(target, "boot", "extlinux.conf")
	if err := os.WriteFile(conf, []byte("DEFAULT menu.c32\nPROMPT 0\nMENU HIDDEN\nTIMEOUT 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runFix(t, target, extlinuxTimeoutFix)
	runFix(t, target, extlinuxTimeoutFix)

	b, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "TOTALTIMEOUT"); n != 1 {
		t.Errorf("TOTALTIMEOUT appears %d times, want 1:\n%s", n, b)
	}
}

// The installed fstab carries stoat's work share as "work /work 9p ... 0 2":
// setup-disk writes the target's fstab from the live system's mounts, and
// stoat mounts its shares under /mnt, the directory setup-disk mounts the
// target on. fsck then looks for fsck.9p and /work does not exist, so the
// guest reports a failed mount on every boot.
func TestWorkMountFixRepairsTheInstalledFstab(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	fstab := filepath.Join(target, "etc", "fstab")
	body := "/dev/vda3 / ext4 rw,relatime 0 1\n" +
		"work /work 9p trans=virtio,version=9p2000.L,rw,nofail 0 2\n"
	if err := os.WriteFile(fstab, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	runFix(t, target, workMountFix)
	runFix(t, target, workMountFix)

	b, err := os.ReadFile(fstab)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "work /mnt/work 9p") {
		t.Errorf("fstab does not point work at /mnt/work:\n%s", got)
	}
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 6 && f[2] == "9p" && f[5] != "0" {
			t.Errorf("9p line has fsck pass %q, want 0: %s", f[5], line)
		}
	}
	if !strings.Contains(got, "/dev/vda3 / ext4 rw,relatime 0 1") {
		t.Errorf("the root line was rewritten:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(target, "mnt", "work")); err != nil {
		t.Errorf("mountpoint was not created: %v", err)
	}
}

// setup-disk -m sys copies the live /etc onto the target, so the installer
// banner is still what the installed system's login screen shows.
func TestIssueFixRestoresTheStockBanner(t *testing.T) {
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	issue := filepath.Join(target, "etc", "issue")
	if err := os.WriteFile(issue, []byte(installIssue), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "etc", "alpine-release"), []byte("3.22.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runFix(t, target, issueFix)

	b, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "Installing Alpine") {
		t.Errorf("the installer banner survived:\n%s", got)
	}
	if !strings.Contains(got, "Welcome to Alpine Linux 3.22") {
		t.Errorf("issue = %q, want the stock banner", got)
	}

	// A second run must leave a banner the user may have edited alone.
	if err := os.WriteFile(issue, []byte("my own banner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runFix(t, target, issueFix)
	b, err = os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "my own banner\n" {
		t.Errorf("issue = %q, want the user's own banner untouched", b)
	}
}
