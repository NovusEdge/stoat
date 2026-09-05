package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/novusedge/stoat/internal/gitx"
	"github.com/novusedge/stoat/internal/recipes"
)

// ErrLockOutOfDate identifies a project declaration that is not pinned.
var ErrLockOutOfDate = errors.New("stoat.lock is out of date")

// SyncRecipes validates the current project pin and repairs its cache only
// when a lock entry is missing or no longer matches the active checkout.
// Global recipe state is not touched by an apply in a non-project directory.
func SyncRecipes() error {
	scope, err := recipes.ScopeFor(false)
	if err != nil {
		return err
	}
	if scope.Name != "project" {
		return nil
	}
	if _, stale, err := recipes.StaleLock(scope); err != nil {
		return err
	} else if stale {
		return fmt.Errorf("%w; run stoat recipe lock", ErrLockOutOfDate)
	}
	lock, err := scope.Lock()
	if err != nil {
		return err
	}
	fresh, err := cacheCurrent(scope, lock)
	if err != nil {
		return err
	}
	if fresh {
		return nil
	}
	return recipes.Sync(scope)
}

func cacheCurrent(scope recipes.Scope, lock recipes.Lock) (bool, error) {
	entries, err := os.ReadDir(scope.CachePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if os.IsNotExist(err) {
		return len(lock.Recipes) == 0, nil
	}
	for name, entry := range lock.Recipes {
		path := filepath.Join(scope.CachePath, name)
		have, err := gitx.RevParse(path, "HEAD")
		if err != nil || have != entry.Commit {
			return false, nil
		}
		if err := recipes.ValidateTree(path, name); err != nil {
			return false, nil
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "." || entry.Name() == ".." {
			continue
		}
		if _, ok := lock.Recipes[entry.Name()]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// RecipeUsers returns sorted VM names that list name in their recipe set.
func RecipeUsers(name string) ([]string, error) {
	vms, err := List()
	if err != nil {
		return nil, err
	}
	users := make([]string, 0)
	for _, vm := range vms {
		for _, recipe := range vm.Recipes {
			if recipe == name {
				users = append(users, vm.Name)
				break
			}
		}
	}
	sort.Strings(users)
	return users, nil
}
