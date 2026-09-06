//go:build linux

package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKVMCheckAt(t *testing.T) {
	dir := t.TempDir()

	writable := filepath.Join(dir, "kvm-ok")
	if err := os.WriteFile(writable, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if c := kvmCheckAt(writable); !c.OK {
		t.Errorf("a read/write file should pass: %+v", c)
	} else if len(c.Fix) != 0 {
		t.Errorf("a passing check must not carry a Fix, got %v", c.Fix)
	}

	missing := filepath.Join(dir, "does-not-exist")
	c := kvmCheckAt(missing)
	if c.OK {
		t.Error("a missing device should fail")
	}
	if !strings.Contains(c.Detail, "not present") {
		t.Errorf("Detail = %q, want it to mention the device is not present", c.Detail)
	}

	denied := filepath.Join(dir, "kvm-denied")
	if err := os.WriteFile(denied, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	// root ignores file permissions, so this case is only meaningful unprivileged.
	if os.Geteuid() != 0 {
		c := kvmCheckAt(denied)
		if c.OK {
			t.Error("an unreadable device should fail")
		}
		if c.Detail != "permission denied" {
			t.Errorf("Detail = %q, want %q", c.Detail, "permission denied")
		}
		if len(c.Fix) == 0 {
			t.Error("a permission failure must carry the usermod fix")
		}
		if !strings.Contains(strings.Join(c.Fix, " "), "usermod -aG kvm") {
			t.Errorf("Fix = %v, want the usermod command", c.Fix)
		}
	}
}

// TestKVMCheckAtOtherError drives kvmCheckAt's default branch: an error
// that is neither fs.ErrNotExist nor fs.ErrPermission. A path whose parent
// is a regular file, not a directory, reliably yields ENOTDIR.
func TestKVMCheckAtOtherError(t *testing.T) {
	dir := t.TempDir()

	notADir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(notADir, nil, 0o666); err != nil {
		t.Fatal(err)
	}

	c := kvmCheckAt(filepath.Join(notADir, "kvm"))
	if c.OK {
		t.Error("a path through a non-directory should fail")
	}
	if c.Detail == "" || strings.Contains(c.Detail, "not present") || c.Detail == "permission denied" {
		t.Errorf("Detail = %q, want the underlying error text, not the not-exist/permission-denied cases", c.Detail)
	}
}
