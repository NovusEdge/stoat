package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharesResolve(t *testing.T) {
	dir := write(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"a\"\nshares = [\".\", \"src\"]\n")
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Shares("dev")
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Share{
		{Tag: "p0", Host: real, Guest: "/work"},
		{Tag: "p1", Host: filepath.Join(real, "src"), Guest: "/work/src"},
	}
	if len(got) != len(want) {
		t.Fatalf("shares = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("share %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestShareOutsideTheProjectIsRefused(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\nshares = [\"../secrets\"]\n")
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Shares("dev")
	if err == nil {
		t.Fatal("Shares accepted a path outside the project")
	}
	want := `stoat.toml: vms.dev.shares: "../secrets" is outside the project`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// A symlink is the escape the string check alone misses: "link" is a plain
// relative name and only EvalSymlinks shows where it lands.
func TestShareSymlinkEscapeIsRefused(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\nshares = [\"link\"]\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Shares("dev"); err == nil {
		t.Fatal("Shares followed a symlink out of the project")
	}
}

func TestTwoSharesWithOneBasenameAreRefused(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\nshares = [\"a/lib\", \"b/lib\"]\n")
	for _, d := range []string{"a/lib", "b/lib"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Shares("dev"); err == nil {
		t.Fatal("Shares accepted two entries mounting at one guest path")
	}
}
