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

func rewriteGitURLToBareRepo(t *testing.T, prefix, repo string) {
	t.Helper()
	emptyGlobal := filepath.Join(t.TempDir(), "empty.gitconfig")
	if err := os.WriteFile(emptyGlobal, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	base := "file://" + filepath.ToSlash(filepath.Dir(repo)) + "/"
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", emptyGlobal)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+base+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", prefix)
}

func TestParseRefSplitsIndexNamesURLsAndSCPLikeSources(t *testing.T) {
	tests := []struct {
		input, source, ref string
		url                bool
	}{
		{input: "tailscale", source: "tailscale"},
		{input: "tailscale@v1.2", source: "tailscale", ref: "v1.2"},
		{input: "tailscale@feature/x", source: "tailscale", ref: "feature/x"},
		{input: "https://example.test/stoat-demo@v1.2", source: "https://example.test/stoat-demo", ref: "v1.2", url: true},
		{input: "https://example.test/x/stoat-demo@feature/x", source: "https://example.test/x/stoat-demo", ref: "feature/x", url: true},
		{input: "ssh://git@example.test/x/stoat-demo.git", source: "ssh://git@example.test/x/stoat-demo.git", url: true},
		{input: "ssh://git@example.test/x/stoat-demo.git@feature/topic", source: "ssh://git@example.test/x/stoat-demo.git", ref: "feature/topic", url: true},
		{input: "https://user@example.test/x/stoat-demo.git", source: "https://user@example.test/x/stoat-demo.git", url: true},
		{input: "https://user@example.test/x/stoat-demo.git@feature/topic", source: "https://user@example.test/x/stoat-demo.git", ref: "feature/topic", url: true},
		{input: "git@example.test:x/stoat-demo.git", source: "git@example.test:x/stoat-demo.git", url: true},
		{input: "git@example.test:x/stoat-demo.git@main", source: "git@example.test:x/stoat-demo.git", ref: "main", url: true},
		{input: "git@example.test:x/stoat-demo.git@feature/x", source: "git@example.test:x/stoat-demo.git", ref: "feature/x", url: true},
		{input: "./x/stoat-demo.git@feature/x", source: "./x/stoat-demo.git", ref: "feature/x", url: true},
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
	if strings.Contains(readFile(t, filepath.Join(home, "stoat.lock")), "name =") {
		t.Fatal("caller-only LockEntry.Name was serialized into stoat.lock")
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
		name       string
		inputKind  string
		wantSource string
		wantRef    string
	}{
		{name: "unknown index name", inputKind: "unknown"},
		{name: "missing ref over HTTPS", inputKind: "https-missing-ref", wantSource: "https://github.com/x/stoat-tailscale", wantRef: "does-not-exist"},
		{name: "missing ref over scp", inputKind: "scp-missing-ref", wantSource: "git@github.com:x/stoat-tailscale", wantRef: "does-not-exist"},
		{name: "transport failure", inputKind: "transport", wantSource: "missing.git", wantRef: "main"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			remoteRoot(t)
			input := "tailscal"
			switch tc.inputKind {
			case "https-missing-ref", "scp-missing-ref":
				src := namedRecipeRepo(t, "stoat-tailscale", "stoat-tailscale")
				repo := filepath.Join(filepath.Dir(src), "stoat-tailscale")
				if err := os.Rename(src, repo); err != nil {
					t.Fatal(err)
				}
				prefix := "https://github.com/x/"
				if tc.inputKind == "scp-missing-ref" {
					prefix = "git@github.com:x/"
				}
				rewriteGitURLToBareRepo(t, prefix, repo)
				input = tc.wantSource + "@" + tc.wantRef
			case "transport":
				input = tc.wantSource + "@" + tc.wantRef
			}
			s, err := ScopeFor(true)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Add(s, input, false)
			if err == nil {
				t.Fatalf("Add(%q) = nil, want an error", input)
			}
			switch tc.inputKind {
			case "unknown":
				want := `no recipe "tailscal" in the index; run stoat recipe search tailscal`
				if err.Error() != want {
					t.Fatalf("Add(%q) = %q, want %q", input, err, want)
				}
			case "https-missing-ref", "scp-missing-ref":
				want := `x/stoat-tailscale: no tag or branch "does-not-exist"`
				if err.Error() != want {
					t.Fatalf("Add(%q) = %q, want exact missing-ref error %q", input, err, want)
				}
			case "transport":
				for _, want := range []string{"git clone", tc.wantSource, tc.wantRef} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("Add(%q) = %q, want transport context %q", input, err, want)
					}
				}
				if strings.Contains(err.Error(), "no tag or branch") {
					t.Fatalf("transport failure was misclassified as a missing ref: %v", err)
				}
			}
		})
	}
}

func TestAddValidationFailurePreservesProjectState(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeFile(t, filepath.Join(project, "stoat.toml"), "[project]\nname = \"kept\"\n\n[recipes]\n")
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

	// A malformed alternate declaration target must reject Add while preparing
	// declaration state, before any active artifact is published.
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

func TestProjectAddPreservesGitignoreAndLeavesNoRootCoordinationFile(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	writeFile(t, filepath.Join(project, "stoat.toml"), "[project]\nname = \"keep\"\n\n[recipes]\n")
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitignore := filepath.Join(project, ".gitignore")
	if err := os.WriteFile(gitignore, []byte("keep/\n"), 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gitignore, 0o660); err != nil {
		t.Fatal(err)
	}
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(gitignore)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf(".gitignore mode = %o, want 660", info.Mode().Perm())
	}
	body := readFile(t, gitignore)
	if body != "keep/\n.stoat/\n" || strings.Count(body, ".stoat/") != 1 {
		t.Fatalf(".gitignore = %q, want preserved content and one .stoat/ line", body)
	}
	if _, err := os.Stat(filepath.Join(project, ".stoat-recipe.lock")); !os.IsNotExist(err) {
		t.Fatalf("root coordination artifact stat = %v, want absent", err)
	}
}

type transactionSnapshot struct {
	lock, project, gitignore, cache              []byte
	lockMode, projectMode, ignoreMode, cacheMode os.FileMode
}

func setupTransactionalAdd(t *testing.T) (Scope, string, transactionSnapshot) {
	t.Helper()
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	projectPath := filepath.Join(project, "stoat.toml")
	writeFile(t, projectPath, "[project]\nname = \"keep\"\n\n[recipes]\n")
	if err := os.Chmod(projectPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ignorePath := filepath.Join(project, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("keep/\n"), 0o660); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ignorePath, 0o660); err != nil {
		t.Fatal(err)
	}
	src := namedRecipeRepo(t, "demo", "demo")
	scope, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(scope, src, false); err != nil {
		t.Fatal(err)
	}
	testutil.GitCommit(t, src, map[string]string{"install.sh": "#!/bin/sh\necho v2\n"}, "")
	cachePath := filepath.Join(scope.CachePath, "demo", "install.sh")
	snapshot := transactionSnapshot{
		lock:      readBytes(t, scope.LockPath),
		project:   readBytes(t, scope.ConfigPath),
		gitignore: readBytes(t, ignorePath),
		cache:     readBytes(t, cachePath),
	}
	snapshot.lockMode = fileMode(t, scope.LockPath)
	snapshot.projectMode = fileMode(t, scope.ConfigPath)
	snapshot.ignoreMode = fileMode(t, ignorePath)
	snapshot.cacheMode = fileMode(t, cachePath)
	return scope, src, snapshot
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func assertTransactionSnapshot(t *testing.T, s Scope, before transactionSnapshot) {
	t.Helper()
	if got := readBytes(t, s.LockPath); !reflect.DeepEqual(got, before.lock) {
		t.Fatal("lock bytes changed during failed transaction")
	}
	if got := readBytes(t, s.ConfigPath); !reflect.DeepEqual(got, before.project) {
		t.Fatal("project declaration changed during failed transaction")
	}
	if got := readBytes(t, filepath.Join(s.Dir, ".gitignore")); !reflect.DeepEqual(got, before.gitignore) {
		t.Fatal(".gitignore changed during failed transaction")
	}
	if got := readBytes(t, filepath.Join(s.CachePath, "demo", "install.sh")); !reflect.DeepEqual(got, before.cache) {
		t.Fatal("active cache changed during failed transaction")
	}
	if got := fileMode(t, s.LockPath); got != before.lockMode {
		t.Fatalf("lock mode = %o, want %o", got, before.lockMode)
	}
	if got := fileMode(t, s.ConfigPath); got != before.projectMode {
		t.Fatalf("project mode = %o, want %o", got, before.projectMode)
	}
	if got := fileMode(t, filepath.Join(s.Dir, ".gitignore")); got != before.ignoreMode {
		t.Fatalf(".gitignore mode = %o, want %o", got, before.ignoreMode)
	}
	if got := fileMode(t, filepath.Join(s.CachePath, "demo", "install.sh")); got != before.cacheMode {
		t.Fatalf("cache script mode = %o, want %o", got, before.cacheMode)
	}
}

func hasRecoverableBackup(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stoat-recipe-backup-") {
			return true
		}
	}
	return false
}

func TestAddPostPublicationFailuresRestoreOrReportCommittedState(t *testing.T) {
	t.Run("rollback restores every artifact", func(t *testing.T) {
		s, src, before := setupTransactionalAdd(t)
		cacheTarget := filepath.Join(s.CachePath, "demo")
		injected := errors.New("injected cache publication failure")
		transactionRename = func(old, new string) error {
			if new == cacheTarget {
				body, readErr := os.ReadFile(filepath.Join(old, "install.sh"))
				if readErr == nil && strings.Contains(string(body), "v2") {
					return injected
				}
			}
			return os.Rename(old, new)
		}
		t.Cleanup(func() { transactionRename = os.Rename })
		if _, err := Add(s, src, true); err == nil || !errors.Is(err, injected) {
			t.Fatalf("Add() = %v, want injected publication failure", err)
		}
		assertTransactionSnapshot(t, s, before)
	})

	t.Run("rollback failure reports both causes and keeps backup", func(t *testing.T) {
		s, src, before := setupTransactionalAdd(t)
		cacheTarget := filepath.Join(s.CachePath, "demo")
		publicationErr := errors.New("injected cache publication failure")
		rollbackErr := errors.New("injected cache restore failure")
		transactionRename = func(old, new string) error {
			if new == cacheTarget {
				if strings.HasPrefix(filepath.Base(old), ".stoat-recipe-backup-") {
					return rollbackErr
				}
				return publicationErr
			}
			return os.Rename(old, new)
		}
		t.Cleanup(func() { transactionRename = os.Rename })
		err := error(nil)
		_, err = Add(s, src, true)
		if err == nil || !errors.Is(err, publicationErr) || !strings.Contains(err.Error(), rollbackErr.Error()) || !strings.Contains(err.Error(), "rollback failed") {
			t.Fatalf("Add() = %v, want publication and rollback causes", err)
		}
		if _, statErr := os.Stat(cacheTarget); !os.IsNotExist(statErr) {
			t.Fatalf("cache target stat = %v, want absent after failed restore", statErr)
		}
		if !hasRecoverableBackup(t, filepath.Dir(cacheTarget)) {
			t.Fatal("rollback failure did not preserve a recoverable cache backup")
		}
		if !reflect.DeepEqual(readBytes(t, s.LockPath), before.lock) || !reflect.DeepEqual(readBytes(t, s.ConfigPath), before.project) {
			t.Fatal("rollback failure changed an earlier published lock or declaration")
		}
	})

	t.Run("cleanup failure reports committed state and keeps backup", func(t *testing.T) {
		s, src, before := setupTransactionalAdd(t)
		injected := errors.New("injected backup cleanup failure")
		transactionRemoveBackup = func(target, backup string) error {
			if target == s.LockPath {
				return injected
			}
			return os.RemoveAll(backup)
		}
		t.Cleanup(func() {
			transactionRemoveBackup = func(target, backup string) error { _ = target; return os.RemoveAll(backup) }
		})
		err := error(nil)
		_, err = Add(s, src, true)
		if err == nil || !strings.Contains(err.Error(), "published recipe changes") {
			t.Fatalf("Add() = %v, want committed-state cleanup report", err)
		}
		if string(readBytes(t, filepath.Join(s.CachePath, "demo", "install.sh"))) == string(before.cache) {
			t.Fatal("cleanup failure did not leave the new committed cache")
		}
		if !hasRecoverableBackup(t, filepath.Dir(s.LockPath)) {
			t.Fatal("cleanup failure did not leave a recoverable lock backup")
		}
	})
}

func TestRemovePostPublicationFailureRestoresEveryArtifact(t *testing.T) {
	s, _, before := setupTransactionalAdd(t)
	projectTarget := s.ConfigPath
	injected := errors.New("injected project publication failure")
	transactionRename = func(old, new string) error {
		if new == projectTarget {
			body, readErr := os.ReadFile(old)
			if readErr == nil && !strings.Contains(string(body), "demo") {
				return injected
			}
		}
		return os.Rename(old, new)
	}
	t.Cleanup(func() { transactionRename = os.Rename })
	if err := Remove(s, "demo"); err == nil || !errors.Is(err, injected) {
		t.Fatalf("Remove() = %v, want injected publication failure", err)
	}
	assertTransactionSnapshot(t, s, before)
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
	persisted, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persisted, got) {
		t.Fatalf("persisted LockAll result = %+v, want returned %+v", persisted, got)
	}
	if strings.Contains(readFile(t, s.LockPath), "name =") {
		t.Fatal("LockAll persisted a caller-only name field")
	}
	cacheSentinel := filepath.Join(s.CachePath, "unrelated", "keep.txt")
	writeFile(t, cacheSentinel, "keep\n")
	beforeCache := readFile(t, cacheSentinel)
	if _, err := LockAll(s); err != nil {
		t.Fatal(err)
	}
	if afterCache := readFile(t, cacheSentinel); afterCache != beforeCache {
		t.Fatalf("LockAll mutated cache sentinel from %q to %q", beforeCache, afterCache)
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
	writeFile(t, filepath.Join(s.CachePath, ".stray", "recipe.toml"), "name = \".stray\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(s.CachePath, ".stray", "install.sh"), "#!/bin/sh\n")

	if err := Sync(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "demo", "recipe.toml")); err != nil {
		t.Fatalf("demo cache missing after Sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "stray")); !os.IsNotExist(err) {
		t.Fatalf("project stray stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, ".stray")); !os.IsNotExist(err) {
		t.Fatalf("hidden project stray stat = %v, want not exist", err)
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

func TestSyncRefusesDirtyOrBrokenLockOwnedCaches(t *testing.T) {
	for _, brokenGit := range []bool{false, true} {
		name := "dirty cache"
		if brokenGit {
			name = "broken git cache"
		}
		t.Run(name, func(t *testing.T) {
			remoteRoot(t)
			src := namedRecipeRepo(t, "demo", "demo")
			s, err := ScopeFor(true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Add(s, src, false); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(s.CachePath, "demo")
			if brokenGit {
				if err := os.RemoveAll(filepath.Join(cache, ".git")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(cache, "install.sh"), []byte("edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			lockBefore, err := os.ReadFile(s.LockPath)
			if err != nil {
				t.Fatal(err)
			}
			cacheBefore, err := os.ReadFile(filepath.Join(cache, "install.sh"))
			if err != nil {
				t.Fatal(err)
			}
			err = Sync(s)
			if err == nil {
				t.Fatal("Sync() = nil, want a dirty/probe error")
			}
			if !brokenGit && !errors.Is(err, ErrDirty) {
				t.Fatalf("Sync() = %v, want ErrDirty", err)
			}
			if brokenGit && strings.Contains(err.Error(), "local changes") {
				t.Fatalf("broken-git probe was converted to ErrDirty: %v", err)
			}
			if brokenGit && !strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
				t.Fatalf("Sync() = %v, want the original git probe context", err)
			}
			lockAfter, _ := os.ReadFile(s.LockPath)
			cacheAfter, _ := os.ReadFile(filepath.Join(cache, "install.sh"))
			if !reflect.DeepEqual(lockAfter, lockBefore) || !reflect.DeepEqual(cacheAfter, cacheBefore) {
				t.Fatal("dirty/probe failure changed the active lock or cache")
			}
		})
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
	if strings.Contains(readFile(t, s.LockPath), "name =") {
		t.Fatal("caller-only LockEntry.Name was serialized into stoat.lock after Update")
	}
}

func TestAddAcceptsAFullCommitRefAndChecksOutThePinnedCommit(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	commit := testutil.GitCommit(t, src, map[string]string{
		"recipe.toml": "schema = 3\nname = \"demo\"\ndescription = \"commit ref\"\nscript = \"install.sh\"\n",
		"install.sh":  "#!/bin/sh\necho commit\n",
	}, "")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := Add(s, src+"@"+commit, false)
	if err != nil {
		t.Fatalf("Add() with full commit ref = %v", err)
	}
	if entry.Name != "demo" || entry.Ref != commit || entry.Commit != commit {
		t.Fatalf("Add() entry = %+v, want the requested full commit pinned", entry)
	}
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if got := lock.Recipes["demo"]; got.Ref != commit || got.Commit != commit {
		t.Fatalf("persisted commit-ref entry = %+v, want ref and commit %s", got, commit)
	}
	if got := currentHead(t, filepath.Join(s.CachePath, "demo")); got != commit {
		t.Fatalf("cache HEAD = %s, want %s", got, commit)
	}
}

func TestLockAllAcceptsAFullCommitRefAndPersistsThePin(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := namedRecipeRepo(t, "demo", "demo")
	commit := testutil.GitCommit(t, src, map[string]string{
		"install.sh": "#!/bin/sh\necho commit\n",
	}, "")
	writeFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf(
		"[recipes]\ndemo = { source = %q, ref = %q }\n", src, commit))
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := LockAll(s)
	if err != nil {
		t.Fatalf("LockAll() with full commit ref = %v", err)
	}
	entry, ok := got.Recipes["demo"]
	if !ok || entry.Ref != commit || entry.Commit != commit {
		t.Fatalf("LockAll() = %+v, want demo ref and commit %s", got.Recipes, commit)
	}
	persisted, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Recipes["demo"] != entry {
		t.Fatalf("persisted LockAll entry = %+v, want %+v", persisted.Recipes["demo"], entry)
	}
}

func TestUpdateAcceptsAFullCommitRefAndChecksOutTheNewPin(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	commit := testutil.GitCommit(t, src, map[string]string{
		"install.sh": "#!/bin/sh\necho commit\n",
	}, "")
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	entry := lock.Recipes["demo"]
	entry.Ref = commit
	lock.Recipes["demo"] = entry
	if err := s.Save(lock); err != nil {
		t.Fatal(err)
	}

	got, err := Update(s, []string{"demo"})
	if err != nil {
		t.Fatalf("Update() with full commit ref = %v", err)
	}
	if len(got) != 1 || got[0].Name != "demo" || got[0].Ref != commit || got[0].Commit != commit {
		t.Fatalf("Update() = %+v, want demo ref and commit %s", got, commit)
	}
	if got := currentHead(t, filepath.Join(s.CachePath, "demo")); got != commit {
		t.Fatalf("cache HEAD = %s, want %s", got, commit)
	}
	persisted, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Recipes["demo"].Ref != commit || persisted.Recipes["demo"].Commit != commit {
		t.Fatalf("persisted Update entry = %+v, want ref and commit %s", persisted.Recipes["demo"], commit)
	}
}

func TestAddMissingFullCommitRefKeepsSourceAndCommitContext(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	missing := strings.Repeat("f", 40)
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Add(s, src+"@"+missing, false)
	if err == nil {
		t.Fatal("Add() with a missing full commit ref = nil, want an error")
	}
	assertMissingCommitContext(t, err, src, missing)
}

func TestLockAllMissingFullCommitRefKeepsSourceAndCommitContext(t *testing.T) {
	remoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := namedRecipeRepo(t, "demo", "demo")
	missing := strings.Repeat("e", 40)
	writeFile(t, filepath.Join(project, "stoat.toml"), fmt.Sprintf(
		"[recipes]\ndemo = { source = %q, ref = %q }\n", src, missing))
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = LockAll(s)
	if err == nil {
		t.Fatal("LockAll() with a missing full commit ref = nil, want an error")
	}
	assertMissingCommitContext(t, err, src, missing)
}

func TestUpdateMissingFullCommitRefKeepsSourceAndCommitContext(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	missing := strings.Repeat("d", 40)
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	entry := lock.Recipes["demo"]
	entry.Ref = missing
	lock.Recipes["demo"] = entry
	if err := s.Save(lock); err != nil {
		t.Fatal(err)
	}

	_, err = Update(s, []string{"demo"})
	if err == nil {
		t.Fatal("Update() with a missing full commit ref = nil, want an error")
	}
	assertMissingCommitContext(t, err, src, missing)
}

func assertMissingCommitContext(t *testing.T, err error, source, commit string) {
	t.Helper()
	label := strings.TrimSuffix(source, ".git")
	labelWithoutLeadingSlash := strings.TrimPrefix(label, string(filepath.Separator))
	if !strings.Contains(err.Error(), commit) || (!strings.Contains(err.Error(), label) && !strings.Contains(err.Error(), labelWithoutLeadingSlash)) {
		t.Fatalf("missing commit error = %q, want source/ref context %q and %q", err, label, commit)
	}
}

func TestUpdateRebuildsAConfirmedMissingCache(t *testing.T) {
	remoteRoot(t)
	src := namedRecipeRepo(t, "demo", "demo")
	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(s, src, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.CachePath, "demo")); err != nil {
		t.Fatal(err)
	}
	newCommit := testutil.GitCommit(t, src, map[string]string{
		"install.sh": "#!/bin/sh\necho rebuilt\n",
	}, "")

	got, err := Update(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "demo" || got[0].Commit != newCommit {
		t.Fatalf("Update() = %+v, want the rebuilt named pin", got)
	}
	lock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if lock.Recipes["demo"].Commit != newCommit {
		t.Fatalf("persisted commit = %q, want %q", lock.Recipes["demo"].Commit, newCommit)
	}
	if _, err := os.Stat(filepath.Join(s.CachePath, "demo", "recipe.toml")); err != nil {
		t.Fatalf("missing cache was not rebuilt: %v", err)
	}
	if strings.Contains(readFile(t, s.LockPath), "name =") {
		t.Fatal("caller-only LockEntry.Name was serialized after missing-cache Update")
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
	// A malformed declaration must reject Remove before publication. The active
	// lock and cache must remain in place.
	writeFile(t, s.ConfigPath, "[recipes\n")
	if err := Remove(s, "demo"); err == nil {
		t.Fatal("Remove() = nil, want declaration validation failure")
	}
	lockAfter, _ := os.ReadFile(s.LockPath)
	if !reflect.DeepEqual(lockAfter, lockBefore) {
		t.Fatal("Remove failure changed the active lock")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("Remove failure deleted active cache: %v", err)
	}
}
