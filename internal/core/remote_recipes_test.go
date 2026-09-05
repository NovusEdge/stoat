package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/testutil"
)

func coreRemoteRoot(t *testing.T) string {
	t.Helper()
	home := root(t)
	index := testutil.GitRepo(t, map[string]string{"index.toml": "schema = 1\n"})
	t.Setenv("STOAT_INDEX", index)
	return home
}

func coreRecipeRepo(t *testing.T, repoName, manifestName string) string {
	t.Helper()
	src := testutil.GitRepo(t, map[string]string{
		"recipe.toml": fmt.Sprintf("schema = 3\nname = %q\nscript = \"install.sh\"\n", manifestName),
		"install.sh":  "#!/bin/sh\necho v1\n",
	})
	dst := filepath.Join(filepath.Dir(src), repoName+".git")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestSyncRecipesReportsAStaleProjectLock(t *testing.T) {
	coreRemoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte("[recipes]\ndemo = \"main\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := SyncRecipes()
	if !errors.Is(err, ErrLockOutOfDate) || !strings.Contains(err.Error(), "run stoat recipe lock") {
		t.Fatalf("SyncRecipes() = %v, want ErrLockOutOfDate with repair command", err)
	}
}

func TestRecipeUsersReturnsSortedVMNames(t *testing.T) {
	coreRemoteRoot(t)
	t.Chdir(t.TempDir())
	for _, v := range []*config.VM{
		{Name: "zeta", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201, Recipes: []string{"demo"}},
		{Name: "alpha", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2202, Recipes: []string{"demo"}},
		{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2203, Recipes: []string{"other"}},
	} {
		if err := v.Save(); err != nil {
			t.Fatal(err)
		}
	}

	users, err := RecipeUsers("demo")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(users, ","), "alpha,zeta"; got != want {
		t.Fatalf("RecipeUsers(demo) = %q, want %q", got, want)
	}
}

func TestPlanApplyAndNeedsProvisionUseAFreshProjectCacheWithoutSync(t *testing.T) {
	home := coreRemoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := coreRecipeRepo(t, "demo", "demo")
	commit := currentRecipeHead(t, src)
	if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte(fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src)), 0o644); err != nil {
		t.Fatal(err)
	}
	scope, err := recipes.ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	clone := testutil.GitClone(t, src)
	if err := os.MkdirAll(scope.CachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, filepath.Join(scope.CachePath, "demo")); err != nil {
		t.Fatal(err)
	}
	if err := recipes.SaveLock(scope.LockPath, recipes.Lock{Recipes: map[string]recipes.LockEntry{
		"demo": {Source: src, Ref: "main", Commit: commit},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanApply("work", ApplyOpts{})
	if err != nil {
		t.Fatalf("PlanApply() on a current cache = %v; a fresh cache must not fetch", err)
	}
	if len(plan) != 1 || plan[0].Name != "demo" {
		t.Fatalf("PlanApply() = %+v, want demo", plan)
	}
	need, err := NeedsProvision(v)
	if err != nil {
		t.Fatalf("NeedsProvision() on a current cache = %v", err)
	}
	if !need {
		t.Fatal("NeedsProvision() = false, want the never-applied recipe to remain work")
	}
	if _, err := os.Stat(filepath.Join(home, "stoat.lock")); !os.IsNotExist(err) {
		t.Fatalf("fresh project planning touched global lock: stat = %v", err)
	}
}

func TestPlanApplyLazilySyncsMissingOrMismatchedProjectCache(t *testing.T) {
	for _, wantMismatch := range []bool{false, true} {
		name := "missing cache"
		if wantMismatch {
			name = "mismatched cache"
		}
		t.Run(name, func(t *testing.T) {
			coreRemoteRoot(t)
			project := t.TempDir()
			t.Chdir(project)
			src := coreRecipeRepo(t, "demo", "demo")
			oldClone := ""
			if wantMismatch {
				oldClone = testutil.GitClone(t, src)
			}
			newCommit := testutil.GitCommit(t, src, map[string]string{"install.sh": "#!/bin/sh\necho v2\n"}, "")
			if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte(fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src)), 0o644); err != nil {
				t.Fatal(err)
			}
			scope, err := recipes.ScopeFor(false)
			if err != nil {
				t.Fatal(err)
			}
			if wantMismatch {
				if err := os.MkdirAll(scope.CachePath, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(oldClone, filepath.Join(scope.CachePath, "demo")); err != nil {
					t.Fatal(err)
				}
			}
			if err := recipes.SaveLock(scope.LockPath, recipes.Lock{Recipes: map[string]recipes.LockEntry{
				"demo": {Source: src, Ref: "main", Commit: newCommit},
			}}); err != nil {
				t.Fatal(err)
			}
			v := &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}
			if err := v.Save(); err != nil {
				t.Fatal(err)
			}

			plan, err := PlanApply("work", ApplyOpts{})
			if err != nil {
				t.Fatalf("PlanApply() = %v, want lazy sync to repair %s", err, name)
			}
			if len(plan) != 1 || plan[0].Name != "demo" {
				t.Fatalf("PlanApply() = %+v, want demo", plan)
			}
			if got := currentRecipeHead(t, filepath.Join(scope.CachePath, "demo")); got != newCommit {
				t.Fatalf("cache HEAD = %s, want lock commit %s", got, newCommit)
			}
		})
	}
}

func TestPlanApplyRefusesDirtyOrBrokenProjectCaches(t *testing.T) {
	for _, brokenGit := range []bool{false, true} {
		name := "dirty cache"
		if brokenGit {
			name = "broken git cache"
		}
		t.Run(name, func(t *testing.T) {
			home := coreRemoteRoot(t)
			project := t.TempDir()
			t.Chdir(project)
			src := coreRecipeRepo(t, "demo", "demo")
			commit := currentRecipeHead(t, src)
			if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte(fmt.Sprintf("[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src)), 0o644); err != nil {
				t.Fatal(err)
			}
			scope, err := recipes.ScopeFor(false)
			if err != nil {
				t.Fatal(err)
			}
			clone := testutil.GitClone(t, src)
			if err := os.MkdirAll(scope.CachePath, 0o755); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(scope.CachePath, "demo")
			if err := os.Rename(clone, cache); err != nil {
				t.Fatal(err)
			}
			if err := recipes.SaveLock(scope.LockPath, recipes.Lock{Recipes: map[string]recipes.LockEntry{
				"demo": {Source: src, Ref: "main", Commit: commit},
			}}); err != nil {
				t.Fatal(err)
			}
			if brokenGit {
				if err := os.RemoveAll(filepath.Join(cache, ".git")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(cache, "install.sh"), []byte("edited\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			cacheBefore, err := os.ReadFile(filepath.Join(cache, "install.sh"))
			if err != nil {
				t.Fatal(err)
			}
			lockBefore, err := os.ReadFile(scope.LockPath)
			if err != nil {
				t.Fatal(err)
			}
			v := &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}
			if err := v.Save(); err != nil {
				t.Fatal(err)
			}
			_, err = PlanApply("work", ApplyOpts{})
			if err == nil {
				t.Fatal("PlanApply() = nil, want dirty/probe error")
			}
			if !brokenGit && !errors.Is(err, recipes.ErrDirty) {
				t.Fatalf("PlanApply() = %v, want recipes.ErrDirty", err)
			}
			if brokenGit && strings.Contains(err.Error(), "local changes") {
				t.Fatalf("broken-git PlanApply was converted to ErrDirty: %v", err)
			}
			if brokenGit && !strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
				t.Fatalf("PlanApply() = %v, want the original git probe context", err)
			}
			if _, statErr := os.Stat(filepath.Join(home, "stoat.lock")); !os.IsNotExist(statErr) {
				t.Fatalf("PlanApply touched global lock: %v", statErr)
			}
			cacheAfter, _ := os.ReadFile(filepath.Join(cache, "install.sh"))
			lockAfter, _ := os.ReadFile(scope.LockPath)
			if string(cacheAfter) != string(cacheBefore) || string(lockAfter) != string(lockBefore) {
				t.Fatal("PlanApply dirty/probe failure changed active cache or lock")
			}
		})
	}
}

func TestProjectReadersRemoveHiddenUnlockedCacheEntries(t *testing.T) {
	for _, reader := range []string{"SyncRecipes", "PlanApply"} {
		t.Run(reader, func(t *testing.T) {
			coreRemoteRoot(t)
			project := t.TempDir()
			t.Chdir(project)
			src := coreRecipeRepo(t, "demo", "demo")
			commit := currentRecipeHead(t, src)
			if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte(fmt.Sprintf(
				"[recipes]\ndemo = { source = %q, ref = \"main\" }\n", src)), 0o644); err != nil {
				t.Fatal(err)
			}
			scope, err := recipes.ScopeFor(false)
			if err != nil {
				t.Fatal(err)
			}
			clone := testutil.GitClone(t, src)
			if err := os.MkdirAll(scope.CachePath, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(clone, filepath.Join(scope.CachePath, "demo")); err != nil {
				t.Fatal(err)
			}
			if err := recipes.SaveLock(scope.LockPath, recipes.Lock{Recipes: map[string]recipes.LockEntry{
				"demo": {Source: src, Ref: "main", Commit: commit},
			}}); err != nil {
				t.Fatal(err)
			}
			writeCoreRemoteFile(t, filepath.Join(scope.CachePath, ".stray", "recipe.toml"), "name = \".stray\"\nscript = \"install.sh\"\n")
			writeCoreRemoteFile(t, filepath.Join(scope.CachePath, ".stray", "install.sh"), "#!/bin/sh\n")
			v := &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}
			if err := v.Save(); err != nil {
				t.Fatal(err)
			}

			if reader == "SyncRecipes" {
				err = SyncRecipes()
			} else {
				_, err = PlanApply("work", ApplyOpts{})
			}
			if err != nil {
				t.Fatalf("%s() = %v, want one successful repair", reader, err)
			}
			if _, err := os.Stat(filepath.Join(scope.CachePath, ".stray")); !os.IsNotExist(err) {
				t.Fatalf("hidden cache stray stat = %v, want not exist after %s", err, reader)
			}
			if _, err := os.Stat(filepath.Join(scope.CachePath, "demo", "recipe.toml")); err != nil {
				t.Fatalf("current cache disappeared after %s: %v", reader, err)
			}
		})
	}
}

func TestPlanApplyBlocksAcrossAnIntermediateProjectRemoval(t *testing.T) {
	coreRemoteRoot(t)
	project := t.TempDir()
	t.Chdir(project)
	src := coreRecipeRepo(t, "demo", "demo")
	if err := os.WriteFile(filepath.Join(project, "stoat.toml"), []byte("[recipes]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scope, err := recipes.ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recipes.Add(scope, src, false); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"demo"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	coordPath := filepath.Join(filepath.Dir(scope.CachePath), "recipe.lock")
	coord, err := os.OpenFile(coordPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = coord.Close() }()
	if err := syscall.Flock(int(coord.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(coord.Fd()), syscall.LOCK_UN) }()
	lockBackup := filepath.Join(t.TempDir(), "old.lock")
	if err := os.Rename(scope.LockPath, lockBackup); err != nil {
		t.Fatal(err)
	}

	type planResult struct {
		plan []ApplyPlan
		err  error
	}
	done := make(chan planResult, 1)
	go func() {
		plan, planErr := PlanApply("work", ApplyOpts{})
		done <- planResult{plan: plan, err: planErr}
	}()
	select {
	case got := <-done:
		t.Fatalf("PlanApply returned while project removal lock was held: plan=%+v err=%v", got.plan, got.err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := scope.RemoveDecl("demo"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(scope.CachePath, "demo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lockBackup); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(coord.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	var got planResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PlanApply did not finish after the project removal lock was released")
	}
	if got.err == nil || errors.Is(got.err, ErrLockOutOfDate) || !errors.Is(got.err, ErrRecipeNotApplicable) {
		t.Fatalf("PlanApply after complete removal = plan=%+v err=%v, want the final missing-recipe result", got.plan, got.err)
	}
}

func writeCoreRemoteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRecipesOutsideAProjectDoesNotTouchGlobalCache(t *testing.T) {
	home := coreRemoteRoot(t)
	t.Chdir(t.TempDir())
	if err := SyncRecipes(); err != nil {
		t.Fatalf("SyncRecipes() outside project = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, "stoat.lock")); !os.IsNotExist(err) {
		t.Fatalf("global lock stat = %v, want no project gate write", err)
	}
	if _, err := os.Stat(filepath.Join(home, "recipes")); !os.IsNotExist(err) {
		t.Fatalf("global cache stat = %v, want no project gate write", err)
	}
}

func currentRecipeHead(t *testing.T, dir string) string {
	t.Helper()
	work := testutil.GitClone(t, dir)
	b, err := os.ReadFile(filepath.Join(work, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(string(b))
	if !strings.HasPrefix(head, "ref: ") {
		return head
	}
	ref := strings.TrimPrefix(head, "ref: ")
	b, err = os.ReadFile(filepath.Join(work, ".git", filepath.FromSlash(ref)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
