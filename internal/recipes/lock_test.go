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
	dir := t.TempDir()
	path := filepath.Join(dir, "stoat.lock")
	old := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"old": {Source: "source", Ref: "main", Commit: "old", Added: "now"},
	}}
	if err := SaveLock(path, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}

	newLock := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"new": {Source: "source", Ref: "main", Commit: "new", Added: "now"},
	}}
	if err := SaveLock(path, newLock); err == nil {
		t.Fatal("SaveLock unexpectedly replaced a lock after staging failed")
	}
	got, err := LoadLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Recipes["old"] != old.Recipes["old"] {
		t.Errorf("previous lock = %+v, want %+v", got.Recipes, old.Recipes)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("failed staging file remains: %v", err)
	}
}
