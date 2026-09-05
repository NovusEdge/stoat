package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLockRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stoat.lock")
	want := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"tailscale": {
			Source: "https://github.com/x/stoat-tailscale",
			Ref:    "v1.2",
			Commit: "9f3c1e2a7b0000000000000000000000000000ab",
			Added:  "2026-09-04T10:00:00Z",
		},
	}}
	if err := SaveLock(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != want.Schema {
		t.Errorf("schema = %d, want %d", got.Schema, want.Schema)
	}
	if got.Recipes["tailscale"] != want.Recipes["tailscale"] {
		t.Errorf("entry = %+v, want %+v", got.Recipes["tailscale"], want.Recipes["tailscale"])
	}
	b := readFile(t, path)
	if !strings.HasPrefix(b, "# stoat.lock: written by stoat; do not edit\n") {
		t.Errorf("lock has no header comment:\n%s", b)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("new lock mode = %o, want 644", got)
	}
}

func TestLoadLockMissingFileIsEmpty(t *testing.T) {
	l, err := LoadLock(filepath.Join(t.TempDir(), "stoat.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Recipes) != 0 {
		t.Errorf("recipes = %v, want empty", l.Recipes)
	}
}

func TestLoadLockRejectsANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stoat.lock")
	writeFile(t, path, "schema = 2\n")
	_, err := LoadLock(path)
	if err == nil || !strings.Contains(err.Error(), "schema 2 is newer than this stoat (1)") {
		t.Fatalf("err = %v, want the newer-schema message", err)
	}
}

func TestSaveLockLeavesPreviousLockWhenReplacementFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based replacement failure is unavailable to root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "stoat.lock")
	foreignPath := filepath.Join(dir, "keep.me")
	foreign := []byte("caller-owned\n")
	old := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"old": {Source: "source", Ref: "main", Commit: "old", Added: "now"},
	}}
	if err := SaveLock(path, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignPath, foreign, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("initial lock is empty")
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Error(err)
		}
	})

	newLock := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"new": {Source: "source", Ref: "main", Commit: "new", Added: "now"},
	}}
	if err := SaveLock(path, newLock); err == nil {
		t.Fatal("SaveLock unexpectedly replaced a lock after staging failed")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Errorf("previous lock bytes changed from %q to %q", before, got)
	}
	got, err = os.ReadFile(foreignPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(foreign) {
		t.Errorf("foreign artifact changed from %q to %q", foreign, got)
	}
}
