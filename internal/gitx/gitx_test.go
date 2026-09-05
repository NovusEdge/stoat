package gitx_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/gitx"
	"github.com/novusedge/stoat/internal/testutil"
)

func TestCloneAtTagAndRevParse(t *testing.T) {
	bare := testutil.GitRepo(t, map[string]string{"recipe.toml": "name = \"demo\"\n"})
	want := testutil.GitCommit(t, bare, map[string]string{"install.sh": "echo hi\n"}, "v1.2")

	dst := filepath.Join(t.TempDir(), "demo")
	if err := gitx.Clone(bare, "v1.2", dst); err != nil {
		t.Fatal(err)
	}
	got, err := gitx.RevParse(dst, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("HEAD = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dst, "install.sh")); err != nil {
		t.Error(err)
	}
}

func TestCloneUnknownRef(t *testing.T) {
	bare := testutil.GitRepo(t, map[string]string{"recipe.toml": "name = \"demo\"\n"})
	err := gitx.Clone(bare, "v9", filepath.Join(t.TempDir(), "demo"))
	if !errors.Is(err, gitx.ErrNoRef) {
		t.Fatalf("err = %v, want ErrNoRef", err)
	}
}

func TestFetchUnknownRef(t *testing.T) {
	bare := testutil.GitRepo(t, map[string]string{"recipe.toml": "name = \"demo\"\n"})
	dst := testutil.GitClone(t, bare)
	err := gitx.Fetch(dst, "v9")
	if !errors.Is(err, gitx.ErrNoRef) {
		t.Fatalf("err = %v, want ErrNoRef", err)
	}
}

func TestDirtyReportsAnEditedWorkTree(t *testing.T) {
	bare := testutil.GitRepo(t, map[string]string{"recipe.toml": "name = \"demo\"\n"})
	dst := testutil.GitClone(t, bare)
	dirty, err := gitx.Dirty(dst)
	if err != nil || dirty {
		t.Fatalf("Dirty = %v, %v, want false, nil", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(dst, "recipe.toml"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = gitx.Dirty(dst)
	if err != nil || !dirty {
		t.Fatalf("Dirty = %v, %v, want true, nil", dirty, err)
	}
}
