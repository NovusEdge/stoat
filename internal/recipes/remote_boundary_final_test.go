package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/testutil"
)

func unsafeExternalRecipeNames() []string {
	return []string{"", ".", "..", ".hidden", "recipe/part", "../escape", "recipe name", "recipe\\part"}
}

func writeRawLock(t *testing.T, path, name, source, ref, commit string) {
	t.Helper()
	writeFile(t, path, fmt.Sprintf(
		"schema = 1\n\n[recipes.%q]\nsource = %q\nref = %q\ncommit = %q\nadded = \"2026-09-05T00:00:00Z\"\n",
		name, source, ref, commit,
	))
}

func assertOutsideRecipeSentinels(t *testing.T, before map[string]string) {
	t.Helper()
	for path, want := range before {
		if got := readFile(t, path); got != want {
			t.Fatalf("outside-cache sentinel %s changed from %q to %q", path, want, got)
		}
	}
}

func assertRejectedBeforeGit(t *testing.T, err error, source string) {
	t.Helper()
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "git clone") || (source != "" && strings.Contains(err.Error(), source)) {
		t.Fatalf("rejection happened after a Git attempt: %v", err)
	}
}

func TestRefreshIndexRejectsMalformedDecodedNamesBeforePublication(t *testing.T) {
	for _, name := range unsafeExternalRecipeNames() {
		t.Run(fmt.Sprintf("name-%q", name), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("STOAT_HOME", home)
			t.Chdir(t.TempDir())
			valid := testutil.GitRepo(t, map[string]string{
				"index.toml": "schema = 1\n\n[recipes.good]\nsource = \"local\"\ndescription = \"good\"\nos = [\"alpine\"]\n",
			})
			t.Setenv("STOAT_INDEX", valid)
			if err := RefreshIndex(true); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(home, "escape", "keep.txt")
			writeFile(t, sentinel, "caller-owned\n")
			malformed := testutil.GitRepo(t, map[string]string{
				"index.toml": fmt.Sprintf(
					"schema = 1\n\n[recipes.%q]\nsource = \"local\"\ndescription = \"bad\"\nos = [\"alpine\"]\n",
					name,
				),
			})
			t.Setenv("STOAT_INDEX", malformed)
			if err := RefreshIndex(true); err == nil {
				t.Fatalf("RefreshIndex() accepted malformed index name %q", name)
			}
			idx, err := LoadIndex()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := idx.Recipes["good"]; !ok {
				t.Fatalf("failed refresh replaced the active index: %+v", idx.Recipes)
			}
			assertOutsideRecipeSentinels(t, map[string]string{sentinel: "caller-owned\n"})
		})
	}
}

func TestIndexLookupRejectsMalformedCallerNamesBeforeFetching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	t.Chdir(t.TempDir())
	missingIndex := filepath.Join(t.TempDir(), "not-a-repository")
	t.Setenv("STOAT_INDEX", missingIndex)
	for _, name := range unsafeExternalRecipeNames() {
		t.Run(fmt.Sprintf("name-%q", name), func(t *testing.T) {
			_, _, err := IndexLookup(name)
			if err == nil {
				t.Fatalf("IndexLookup(%q) accepted malformed caller name", name)
			}
			if strings.Contains(err.Error(), "git clone") || strings.Contains(err.Error(), missingIndex) {
				t.Fatalf("IndexLookup(%q) fetched before rejecting the caller name: %v", name, err)
			}
			if _, statErr := os.Stat(missingIndex); !os.IsNotExist(statErr) {
				t.Fatalf("IndexLookup(%q) created or changed the missing source: %v", name, statErr)
			}
		})
	}
}

func TestRefreshIndexRejectsAnIndexEntryWithoutARequiredSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	t.Chdir(t.TempDir())
	good := testutil.GitRepo(t, map[string]string{
		"index.toml": "schema = 1\n\n[recipes.good]\nsource = \"local\"\ndescription = \"good\"\nos = [\"alpine\"]\n",
	})
	t.Setenv("STOAT_INDEX", good)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	bad := testutil.GitRepo(t, map[string]string{
		"index.toml": "schema = 1\n\n[recipes.bad]\ndescription = \"missing source\"\nos = [\"alpine\"]\n",
	})
	t.Setenv("STOAT_INDEX", bad)
	if err := RefreshIndex(true); err == nil {
		t.Fatal("RefreshIndex() accepted an index entry without source")
	}
	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Recipes["good"]; !ok {
		t.Fatalf("missing-source refresh replaced the active index: %+v", idx.Recipes)
	}
}

func TestRemoteOperationsRejectMalformedExternalNamesBeforePublication(t *testing.T) {
	operations := []struct {
		name string
		call func(Scope, string) error
	}{
		{name: "LockAll", call: func(s Scope, _ string) error { _, err := LockAll(s); return err }},
		{name: "Sync", call: func(s Scope, _ string) error { return Sync(s) }},
		{name: "Update", call: func(s Scope, _ string) error { _, err := Update(s, nil); return err }},
		{name: "Remove", call: func(s Scope, name string) error { return Remove(s, name) }},
	}
	for _, operation := range operations {
		for _, name := range unsafeExternalRecipeNames() {
			t.Run(operation.name+"/name-"+fmt.Sprintf("%q", name), func(t *testing.T) {
				home := remoteRoot(t)
				src := filepath.Join(home, "missing-source.git")
				commit := strings.Repeat("a", 40)
				s, err := ScopeFor(true)
				if err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(home, "escape", "keep.txt")
				writeFile(t, outside, "caller-owned\n")
				writeRawLock(t, s.LockPath, name, src, "main", commit)
				lockBefore := readFile(t, s.LockPath)
				err = operation.call(s, name)
				if err == nil {
					t.Fatalf("%s accepted malformed external name %q", operation.name, name)
				}
				assertRejectedBeforeGit(t, err, src)
				if got := readFile(t, s.LockPath); got != lockBefore {
					t.Fatalf("%s changed the active lock after rejecting %q", operation.name, name)
				}
				assertOutsideRecipeSentinels(t, map[string]string{outside: "caller-owned\n"})
			})
		}
	}
}

func TestRemoteOperationsRejectMissingSourceAndMalformedCommitPins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		commit string
	}{
		{name: "missing source", commit: strings.Repeat("a", 40)},
		{name: "short commit", commit: strings.Repeat("b", 39)},
	} {
		for _, operation := range []struct {
			name string
			call func(Scope) error
		}{
			{name: "Sync", call: Sync},
			{name: "Update", call: func(s Scope) error { _, err := Update(s, nil); return err }},
			{name: "Remove", call: func(s Scope) error { return Remove(s, "demo") }},
		} {
			t.Run(operation.name+"/"+tc.name, func(t *testing.T) {
				home := remoteRoot(t)
				source := ""
				if tc.name == "short commit" {
					source = namedRecipeRepo(t, "demo", "demo")
				}
				s, err := ScopeFor(true)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(home, "escape", "keep.txt"), "caller-owned\n")
				writeRawLock(t, s.LockPath, "demo", source, "main", tc.commit)
				lockBefore := readFile(t, s.LockPath)
				if err := operation.call(s); err == nil {
					t.Fatalf("%s accepted %s", operation.name, tc.name)
				} else {
					assertRejectedBeforeGit(t, err, source)
				}
				if got := readFile(t, s.LockPath); got != lockBefore {
					t.Fatalf("%s changed the active lock after rejecting %s", operation.name, tc.name)
				}
				assertOutsideRecipeSentinels(t, map[string]string{
					filepath.Join(home, "escape", "keep.txt"): "caller-owned\n",
				})
			})
		}
	}
}

func TestGoRemoteRecipeNamesRemainCompatibleWithExistingComponents(t *testing.T) {
	for _, name := range []string{"Foo", "a_b"} {
		t.Run(name, func(t *testing.T) {
			remoteRoot(t)
			src := namedRecipeRepo(t, name, name)
			s, err := ScopeFor(true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Add(s, src, false); err != nil {
				t.Fatalf("Add(%q) = %v, want existing Go-safe recipe names to remain valid", name, err)
			}
			path, scope, ok, err := ResolvePath(name)
			if err != nil || !ok || scope != "global" || filepath.Base(path) != name {
				t.Fatalf("ResolvePath(%q) = %q, %q, %v, %v", name, path, scope, ok, err)
			}
		})
	}
}
