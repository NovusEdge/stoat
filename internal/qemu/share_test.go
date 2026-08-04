package qemu

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestPrepareSharesCreatesWorkDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "w", Dir: filepath.Join(root, "w")}

	if err := prepareShares(v); err != nil {
		t.Fatal(err)
	}
	// QEMU refuses to start if an export path is missing, so this must exist
	// before Args is ever used.
	st, err := os.Stat(v.WorkDir())
	if err != nil {
		t.Fatalf("work dir not created: %v", err)
	}
	if !st.IsDir() {
		t.Error("work dir is not a directory")
	}
}

// A missing share is reported by stoat, not left to a raw QEMU stderr line.
func TestPrepareSharesRejectsMissingShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	v := &config.VM{Name: "w", Dir: filepath.Join(root, "w"), Share: filepath.Join(root, "gone")}

	err := prepareShares(v)
	if err == nil {
		t.Fatal("want an error for a share directory that does not exist")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want the underlying not-exist error to survive wrapping, got %v", err)
	}
}

// Never create the user's share. A typo should be reported, not turned into an
// empty directory that looks like their files vanished.
func TestPrepareSharesDoesNotCreateTheUsersShare(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	missing := filepath.Join(root, "gone")
	v := &config.VM{Name: "w", Dir: filepath.Join(root, "w"), Share: missing}

	_ = prepareShares(v)
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("prepareShares created the user's share directory")
	}
}

func TestPrepareSharesRejectsShareThatIsAFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	f := filepath.Join(root, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "w", Dir: filepath.Join(root, "w"), Share: f}

	if err := prepareShares(v); err == nil {
		t.Fatal("want an error when the share is a regular file")
	}
}

func TestXattrOKOnTempDir(t *testing.T) {
	// Nothing to assert about the failure path here: constructing a
	// filesystem without user.* xattr support needs root to mount one.
	if err := xattrOK(t.TempDir()); err != nil {
		t.Skipf("test filesystem has no xattr support: %v", err)
	}
}
