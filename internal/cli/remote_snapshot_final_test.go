package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/testutil"
)

func TestRecipeListBlocksAcrossAnIntermediateCacheAndLockPublication(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho v1\n")
	scope, err := recipes.ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recipes.Add(scope, src, false); err != nil {
		t.Fatal(err)
	}
	oldLock, err := scope.Lock()
	if err != nil {
		t.Fatal(err)
	}
	oldCommit := oldLock.Recipes["demo"].Commit
	newCommit := testutil.GitCommit(t, src, map[string]string{
		"recipe.toml": "schema = 3\nname = \"demo\"\ndescription = \"demo-v2 recipe\"\nos = [\"alpine\"]\nrequires = [\"git\"]\nscript = \"install.sh\"\n\n[params.channel]\ntype = \"enum\"\nvalues = [\"stable\", \"test\"]\ndefault = \"stable\"\n",
		"install.sh":  "#!/bin/sh\necho v2\n",
	}, "")
	newCache := testutil.GitClone(t, src)
	newLock := recipes.Lock{Schema: oldLock.Schema, Recipes: make(map[string]recipes.LockEntry, len(oldLock.Recipes))}
	for name, entry := range oldLock.Recipes {
		newLock.Recipes[name] = entry
	}
	entry := newLock.Recipes["demo"]
	entry.Commit = newCommit
	newLock.Recipes["demo"] = entry

	coordPath := filepath.Join(scope.Dir, "recipe.lock")
	coord, err := os.OpenFile(coordPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = coord.Close() }()
	if err := syscall.Flock(int(coord.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(coord.Fd()), syscall.LOCK_UN) }()
	cache := filepath.Join(scope.CachePath, "demo")
	oldCache := filepath.Join(t.TempDir(), "old-demo")
	if err := os.Rename(cache, oldCache); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newCache, cache); err != nil {
		t.Fatal(err)
	}

	type listResult struct {
		code int
		out  string
		err  string
	}
	done := make(chan listResult, 1)
	go func() {
		var out, errOut bytes.Buffer
		code := Main([]string{"--json", "recipe", "list"}, "test", nil, &out, &errOut)
		done <- listResult{code: code, out: out.String(), err: errOut.String()}
	}()
	select {
	case got := <-done:
		t.Fatalf("recipe list returned while publication lock was held: code=%d stdout=%q stderr=%q", got.code, got.out, got.err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := recipes.SaveLock(scope.LockPath, newLock); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(coord.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.code != ExitOK {
			t.Fatalf("recipe list exit = %d, stdout=%q, stderr=%q", got.code, got.out, got.err)
		}
		var lines []map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace([]byte(got.out)), []byte{'\n'}) {
			var obj map[string]any
			if err := json.Unmarshal(line, &obj); err != nil {
				t.Fatalf("recipe list line = %q: %v", line, err)
			}
			lines = append(lines, obj)
		}
		if len(lines) != 1 {
			t.Fatalf("recipe list emitted %d result lines: %q", len(lines), got.out)
		}
		data, _ := lines[0]["data"].(map[string]any)
		rows, _ := data["recipes"].([]any)
		var row map[string]any
		for _, raw := range rows {
			candidate, _ := raw.(map[string]any)
			if candidate["name"] == "demo" {
				row = candidate
				break
			}
		}
		if row == nil {
			t.Fatalf("recipe list omitted demo: %v", rows)
		}
		if row["description"] != "demo-v2 recipe" {
			t.Fatalf("recipe list saw an incomplete publication: row=%v, old commit=%s, new commit=%s", row, oldCommit, newCommit)
		}
		if row["commit"] != newCommit[:7] {
			t.Fatalf("recipe list paired the new manifest with the wrong pin: row=%v, want commit %s", row, newCommit[:7])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recipe list did not finish after the publication lock was released")
	}
}
