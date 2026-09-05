package recipes

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/testutil"
)

func remoteRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	t.Chdir(t.TempDir())
	// Every remote-recipe test binds the index to a local bare repository.
	index := testutil.GitRepo(t, map[string]string{"index.toml": "schema = 1\n"})
	t.Setenv("STOAT_INDEX", index)
	return home
}

func namedRecipeRepo(t *testing.T, repoName, manifestName string) string {
	t.Helper()
	src := testutil.GitRepo(t, map[string]string{
		"recipe.toml": fmt.Sprintf("schema = 3\nname = %q\nscript = \"install.sh\"\n", manifestName),
		"install.sh":  "#!/bin/sh\nset -e\necho v1\n",
	})
	dst := filepath.Join(filepath.Dir(src), repoName+".git")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func localIndex(t *testing.T, entries string) string {
	t.Helper()
	index := testutil.GitRepo(t, map[string]string{"index.toml": "schema = 1\n\n" + entries})
	t.Setenv("STOAT_INDEX", index)
	return index
}

func TestParseRefSplitsIndexNamesURLsAndSCPLikeSources(t *testing.T) {
	tests := []struct {
		input, source, ref string
		url                bool
	}{
		{input: "tailscale", source: "tailscale"},
		{input: "tailscale@v1.2", source: "tailscale", ref: "v1.2"},
		{input: "https://example.test/stoat-demo@v1.2", source: "https://example.test/stoat-demo", ref: "v1.2", url: true},
		{input: "git@example.test:x/stoat-demo.git", source: "git@example.test:x/stoat-demo.git", url: true},
		{input: "git@example.test:x/stoat-demo.git@main", source: "git@example.test:x/stoat-demo.git", ref: "main", url: true},
	}
	for _, tc := range tests {
		source, ref, isURL := ParseRef(tc.input)
		if source != tc.source || ref != tc.ref || isURL != tc.url {
			t.Errorf("ParseRef(%q) = %q, %q, %v; want %q, %q, %v", tc.input, source, ref, isURL, tc.source, tc.ref, tc.url)
		}
	}
}

func TestAddFromURLReturnsNameAndPinsTheRequestedTag(t *testing.T) {
	home := remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	commit := testutil.GitCommit(t, src, map[string]string{
		"recipe.toml": "schema = 3\nname = \"demo\"\nscript = \"install.sh\"\n\n[params.channel]\ntype = \"enum\"\nvalues = [\"stable\", \"test\"]\ndefault = \"stable\"\n",
		"install.sh":  "#!/bin/sh\nset -e\necho v2\n",
	}, "v1.2")
	preview, previewDir, err := Preview(src, "v1.2")
	if previewDir != "" {
		defer func() {
			if err := os.RemoveAll(previewDir); err != nil {
				t.Errorf("remove Preview temp dir: %v", err)
			}
		}()
	}
	if err != nil {
		t.Fatalf("Preview() = %v", err)
	}
	if preview.Name != "demo" || preview.Params["channel"].Default != "stable" {
		t.Fatalf("Preview() = %+v, want schema-3 channel params", preview)
	}

	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := Add(s, src+"@v1.2", false)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "demo" || entry.Source != src || entry.Ref != "v1.2" || entry.Commit != commit {
		t.Fatalf("entry = %+v, want name/source/ref/commit for demo", entry)
	}
	lock, err := LoadLock(filepath.Join(home, "stoat.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Recipes["demo"].Commit; got != commit {
		t.Errorf("lock commit = %q, want %q", got, commit)
	}
	path, scope, ok, err := ResolvePath("demo")
	if err != nil || !ok || scope != "global" || filepath.Base(path) != "demo" {
		t.Fatalf("ResolvePath(demo) = %q, %q, %v, %v", path, scope, ok, err)
	}
}

func TestAddByIndexNameUsesTheLocalIndexAndDefaultBranch(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	localIndex(t, fmt.Sprintf("[recipes.demo]\nsource = %q\ndescription = \"demo\"\nos = [\"alpine\"]\n", src))

	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := Add(s, "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "demo" || entry.Source != src || entry.Ref != "" || len(entry.Commit) != 40 {
		t.Fatalf("entry = %+v, want index source and default branch pin", entry)
	}
}

func TestAddReportsUnknownNamesAndMissingRefs(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "unknown index name", ref: "tailscal", want: `no recipe "tailscal" in the index; run stoat recipe search tailscal`},
		{name: "missing ref", ref: "@does-not-exist", want: `no tag or branch "does-not-exist"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			remoteRoot(t)
			src := namedRecipeRepo(t, "demo", "demo")
			input := tc.ref
			if tc.name == "missing ref" {
				input = src + tc.ref
			}
			s, err := ScopeFor(true)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Add(s, input, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Add(%q) = %v, want error containing %q", input, err, tc.want)
			}
		})
	}
}

func TestAddValidationFailurePreservesProjectState(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeFile(t, filepath.Join(project, "stoat.toml"), "[vm]\nname = \"kept\"\n\n[recipes]\n")
	if err := os.Chmod(filepath.Join(project, "stoat.toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, good, false); err != nil {
		t.Fatal(err)
	}

	lockBefore, err := os.ReadFile(s.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	declBefore, err := os.ReadFile(s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cacheScript := filepath.Join(s.CachePath, "demo", "install.sh")
	cacheBefore, err := os.ReadFile(cacheScript)
	if err != nil {
		t.Fatal(err)
	}
	configInfo, err := os.Stat(s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}

	// The source basename still resolves to demo, but its manifest deliberately
	// names another recipe so validation fails after staging.
	bad := namedRecipeRepo(t, "demo", "other")
	if _, err := Add(s, bad, true); err == nil || !strings.Contains(err.Error(), `named "other"`) {
		t.Fatalf("Add(invalid replacement) = %v, want a name mismatch", err)
	}
	lockAfter, _ := os.ReadFile(s.LockPath)
	declAfter, _ := os.ReadFile(s.ConfigPath)
	cacheAfter, _ := os.ReadFile(cacheScript)
	afterInfo, _ := os.Stat(s.ConfigPath)
	if !reflect.DeepEqual(lockAfter, lockBefore) || !reflect.DeepEqual(declAfter, declBefore) || !reflect.DeepEqual(cacheAfter, cacheBefore) {
		t.Fatal("validation failure changed the active lock, declaration, or cache")
	}
	if afterInfo.Mode().Perm() != configInfo.Mode().Perm() {
		t.Fatalf("stoat.toml mode = %o, want %o", afterInfo.Mode().Perm(), configInfo.Mode().Perm())
	}
	if !strings.Contains(string(declAfter), `name = "kept"`) {
		t.Fatal("unrelated project value disappeared")
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "demo", "recipe.toml")); err != nil {
		t.Fatalf("old cache disappeared: %v", err)
	}

	// A persistence failure after clone, validation, and lock publication must
	// be just as transactional as a validation failure. The declaration target
	// is malformed, so SetDecl fails after Save has prepared the new lock.
	brokenConfig := filepath.Join(project, "broken.toml")
	writeFile(t, brokenConfig, "[recipes\n")
	persistenceScope := s
	persistenceScope.ConfigPath = brokenConfig
	if _, err := Add(persistenceScope, good, true); err == nil {
		t.Fatal("Add() with an invalid declaration target = nil, want persistence failure")
	}
	lockAfterPersistence, _ := os.ReadFile(s.LockPath)
	declAfterPersistence, _ := os.ReadFile(s.ConfigPath)
	cacheAfterPersistence, _ := os.ReadFile(cacheScript)
	if !reflect.DeepEqual(lockAfterPersistence, lockBefore) || !reflect.DeepEqual(declAfterPersistence, declBefore) || !reflect.DeepEqual(cacheAfterPersistence, cacheBefore) {
		t.Fatal("persistence failure changed the active lock, declaration, or cache")
	}
}

func TestLockAllUsesDeclarationsAndDropsRemovedEntries(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := namedRecipeRepo(t, "demo", "demo")
	writeFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src))
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveLock(s.LockPath, Lock{Recipes: map[string]LockEntry{
		"gone": {Source: "unused", Ref: "main", Commit: strings.Repeat("b", 40)},
	}}); err != nil {
		t.Fatal(err)
	}

	got, err := LockAll(s)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := got.Recipes["demo"]
	if !ok || len(entry.Commit) != 40 || entry.Source != src || entry.Ref != "main" {
		t.Fatalf("LockAll() = %+v, want demo declaration pinned", got.Recipes)
	}
	if _, ok := got.Recipes["gone"]; ok {
		t.Fatalf("LockAll() retained removed declaration: %+v", got.Recipes)
	}
}

func TestSyncBuildsTheProjectCacheAndRemovesProjectStrays(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := namedRecipeRepo(t, "demo", "demo")
	writeFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src))
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	entry := LockEntry{Source: src, Ref: "main", Commit: currentHead(t, src)}
	if err := SaveLock(s.LockPath, Lock{Recipes: map[string]LockEntry{"demo": entry}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.CachePath, "stray", "recipe.toml"), "name = \"stray\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(s.CachePath, "stray", "install.sh"), "#!/bin/sh\n")

	if err := Sync(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "demo", "recipe.toml")); err != nil {
		t.Fatalf("demo cache missing after Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "stray")); !os.IsNotExist(err) {
		t.Fatalf("project stray stat = %v, want not exist", err)
	}
}

func TestSyncFailureLeavesThePreviouslyActiveCache(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := namedRecipeRepo(t, "demo", "demo")
	writeFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src))
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	oldCommit := currentHead(t, src)
	clone := testutil.GitClone(t, src)
	cache := filepath.Join(s.CachePath, "demo")
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, cache); err != nil {
		t.Fatal(err)
	}
	if err := SaveLock(s.LockPath, Lock{Recipes: map[string]LockEntry{"demo": {
		Source: src, Ref: "main", Commit: oldCommit,
	}}}); err != nil {
		t.Fatal(err)
	}
	newCommit := testutil.GitCommit(t, src, map[string]string{
		"recipe.toml": "schema = 3\nname = \"other\"\nscript = \"install.sh\"\n",
		"install.sh":  "#!/bin/sh\necho broken\n",
	}, "")
	if err := SaveLock(s.LockPath, Lock{Recipes: map[string]LockEntry{"demo": {
		Source: src, Ref: "main", Commit: newCommit,
	}}}); err != nil {
		t.Fatal(err)
	}

	err = Sync(s)
	if err == nil || !strings.Contains(err.Error(), `named "other"`) {
		t.Fatalf("Sync() = %v, want validation failure", err)
	}
	if got := currentHead(t, cache); got != oldCommit {
		t.Fatalf("cache HEAD = %s, want old commit %s after failed Sync", got, oldCommit)
	}
	if got := readFile(t, filepath.Join(cache, "recipe.toml")); !strings.Contains(got, `name = "demo"`) {
		t.Fatalf("active cache manifest changed after failed Sync: %s", got)
	}
}

func TestStaleLockComparesRefAndExplicitSource(t *testing.T) {
	cases := []struct {
		name       string
		lockSource string
		lockRef    string
		wantStale  bool
	}{
		{name: "missing entry", wantStale: true},
		{name: "changed ref", lockRef: "old", wantStale: true},
		{name: "changed source", lockSource: "/different/source", lockRef: "main", wantStale: true},
		{name: "matching pin", lockSource: "/source", lockRef: "main", wantStale: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remoteRoot(t)
			project := t.TempDir()
			t.Chdir(project)
			writeFile(t, filepath.Join(project, "stoat.toml"), "[recipes]\ndemo = { source = \"/source\", ref = \"main\" }\n")
			s, err := ScopeFor(false)
			if err != nil {
				t.Fatal(err)
			}
			entries := map[string]LockEntry{}
			if tc.name != "missing entry" {
				entries["demo"] = LockEntry{Source: tc.lockSource, Ref: tc.lockRef, Commit: strings.Repeat("c", 40)}
			}
			if err := SaveLock(s.LockPath, Lock{Recipes: entries}); err != nil {
				t.Fatal(err)
			}
			name, stale, err := StaleLock(s)
			if err != nil {
				t.Fatal(err)
			}
			if stale != tc.wantStale || (stale && name != "demo") {
				t.Fatalf("StaleLock() = %q, %v; want demo, %v", name, stale, tc.wantStale)
			}
		})
	}
}

func TestGlobalSyncLeavesAHandMadeGitRecipeAlone(t *testing.T) {
	home := remoteRoot(t)
	src := namedRecipeRepo(t, "remote", "remote")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveLock(s.LockPath, Lock{Recipes: map[string]LockEntry{"remote": {
		Source: src, Ref: "main", Commit: currentHead(t, src),
	}}}); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(home, "recipes", "handmade")
	writeFile(t, filepath.Join(local, "recipe.toml"), "schema = 3\nname = \"handmade\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(local, "install.sh"), "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Join(local, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Sync(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "remote", "recipe.toml")); err != nil {
		t.Fatalf("remote cache missing after global Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "recipe.toml")); err != nil {
		t.Fatalf("hand-made local recipe was removed: %v", err)
	}
}

func currentHead(t *testing.T, dir string) string {
	t.Helper()
	work := testutil.GitClone(t, dir)
	// GitCommit's helper already establishes a work tree, but this read-only
	// fixture avoids adding a production git API solely for tests.
	b, err := os.ReadFile(filepath.Join(work, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	ref := strings.TrimSpace(string(b))
	if !strings.HasPrefix(ref, "ref: ") {
		return ref
	}
	refPath := strings.TrimPrefix(ref, "ref: ")
	b, err = os.ReadFile(filepath.Join(work, ".git", filepath.FromSlash(refPath)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestUpdateMovesTheCommitAndReturnsItsName(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := Add(s, src, false)
	if err != nil {
		t.Fatal(err)
	}
	newCommit := testutil.GitCommit(t, src, map[string]string{
		"install.sh": "#!/bin/sh\nset -e\necho newer\n",
	}, "")

	got, err := Update(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "demo" || got[0].Commit != newCommit || got[0].Commit == before.Commit {
		t.Fatalf("Update() = %+v, before = %+v, want the new named pin", got, before)
	}
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if lock.Recipes["demo"].Commit != newCommit {
		t.Fatalf("lock commit = %q, want %q", lock.Recipes["demo"].Commit, newCommit)
	}
}

func TestUpdateRefusesADirtyTreeWithErrDirty(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.CachePath, "demo", "install.sh"), "edited\n")

	_, err = Update(s, nil)
	if !errors.Is(err, ErrDirty) || !strings.Contains(err.Error(), "demo: local changes; copy it to a local recipe first") {
		t.Fatalf("Update() = %v, want ErrDirty with the copy-to-local message", err)
	}

	t.Run("dirty probe error is propagated without overwrite", func(t *testing.T) {
		remoteRoot(t)
		src := namedRecipeRepo(t, "demo", "demo")
		s, err := ScopeFor(true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Add(s, src, false); err != nil {
			t.Fatal(err)
		}
		lockBefore, err := os.ReadFile(s.LockPath)
		if err != nil {
			t.Fatal(err)
		}
		cacheScript := filepath.Join(s.CachePath, "demo", "install.sh")
		cacheBefore, err := os.ReadFile(cacheScript)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(s.CachePath, "demo", ".git")); err != nil {
			t.Fatal(err)
		}
		_, err = Update(s, nil)
		if err == nil || errors.Is(err, ErrDirty) || !strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
			t.Fatalf("Update() after Dirty probe failure = %v, want the git probe error", err)
		}
		lockAfter, _ := os.ReadFile(s.LockPath)
		cacheAfter, _ := os.ReadFile(cacheScript)
		if !reflect.DeepEqual(lockAfter, lockBefore) || !reflect.DeepEqual(cacheAfter, cacheBefore) {
			t.Fatal("Dirty probe failure overwrote the active lock or cache")
		}
	})
}

func TestUpdateIsAllOrNothingAcrossMultipleRecipes(t *testing.T) {
	remoteRoot(t)
	srcA := namedRecipeRepo(t, "a", "a")
	srcB := namedRecipeRepo(t, "b", "b")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, srcA, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, srcB, false); err != nil {
		t.Fatal(err)
	}
	oldA := currentHead(t, filepath.Join(s.CachePath, "a"))
	oldB := currentHead(t, filepath.Join(s.CachePath, "b"))
	lockBefore, err := os.ReadFile(s.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	newA := testutil.GitCommit(t, srcA, map[string]string{"install.sh": "#!/bin/sh\necho newer-a\n"}, "")
	_ = newA
	testutil.GitCommit(t, srcB, map[string]string{
		"recipe.toml": "schema = 3\nname = \"not-b\"\nscript = \"install.sh\"\n",
		"install.sh":  "#!/bin/sh\necho invalid-b\n",
	}, "")

	_, err = Update(s, []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), `named "not-b"`) {
		t.Fatalf("Update(a,b) = %v, want the second validation failure", err)
	}
	if got := currentHead(t, filepath.Join(s.CachePath, "a")); got != oldA {
		t.Fatalf("recipe a moved to %s after all-or-nothing failure; want %s", got, oldA)
	}
	if got := currentHead(t, filepath.Join(s.CachePath, "b")); got != oldB {
		t.Fatalf("recipe b moved to %s after all-or-nothing failure; want %s", got, oldB)
	}
	lockAfter, _ := os.ReadFile(s.LockPath)
	if !reflect.DeepEqual(lockAfter, lockBefore) {
		t.Fatal("multi-update failure changed the active lock")
	}
}

func TestRemoveDropsProjectDeclarationLockAndCache(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeFile(t, filepath.Join(project, "stoat.toml"), "[recipes]\n")
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	if err := Remove(s, "demo"); err != nil {
		t.Fatal(err)
	}
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Recipes["demo"]; ok {
		t.Fatal("lock still pins demo after Remove")
	}
	decls, err := s.Decls()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decls["demo"]; ok {
		t.Fatal("project declaration still contains demo after Remove")
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "demo")); !os.IsNotExist(err) {
		t.Fatalf("cache stat = %v, want not exist", err)
	}
}

func TestRemoveFailureRestoresTheExistingLockAndCache(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeFile(t, filepath.Join(project, "stoat.toml"), "[recipes]\n")
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(s.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(s.CachePath, "demo")
	if _, err := os.Stat(cache); err != nil {
		t.Fatal(err)
	}
	// Force the declaration persistence phase to fail after the lock has been
	// prepared. Remove must restore the prior lock and leave the cache active.
	writeFile(t, s.ConfigPath, "[recipes\n")
	if err := Remove(s, "demo"); err == nil {
		t.Fatal("Remove() = nil, want declaration write failure")
	}
	lockAfter, _ := os.ReadFile(s.LockPath)
	if !reflect.DeepEqual(lockAfter, lockBefore) {
		t.Fatal("Remove failure changed the active lock")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("Remove failure deleted active cache: %v", err)
	}
}
