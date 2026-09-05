package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// RecipeSnapshot is a coherent view of visible manifests and their ownership
// pins. Callers receive only after all relevant scope locks are released.
type RecipeSnapshot struct {
	Roots     []Root
	Manifests []Manifest
	Scopes    map[string]string
	Pins      map[string]LockEntry
}

func scopeCoordinationPath(s Scope) string {
	if s.Name == "project" {
		return filepath.Join(filepath.Dir(s.CachePath), "recipe.lock")
	}
	return filepath.Join(s.Dir, "recipe.lock")
}

func lockScopeMode(s Scope, exclusive bool) (func() error, error) {
	coordinationDir := filepath.Dir(scopeCoordinationPath(s))
	if err := os.MkdirAll(coordinationDir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(scopeCoordinationPath(s), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	lockType := syscall.LOCK_SH
	if exclusive {
		lockType = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), lockType); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

// lockRecipeScopes acquires project and global locks in one total path order.
// Callers release the returned locks in reverse order.
func lockRecipeScopes(exclusive bool) ([]func() error, error) {
	global, err := ScopeFor(true)
	if err != nil {
		return nil, err
	}
	project, err := ScopeFor(false)
	if err != nil {
		return nil, err
	}
	scopes := []Scope{global}
	if project.Name == "project" && scopeCoordinationPath(project) != scopeCoordinationPath(global) {
		scopes = append(scopes, project)
	}
	sort.Slice(scopes, func(i, j int) bool {
		return scopeCoordinationPath(scopes[i]) < scopeCoordinationPath(scopes[j])
	})
	locks := make([]func() error, 0, len(scopes))
	for _, scope := range scopes {
		unlock, lockErr := lockScopeMode(scope, exclusive)
		if lockErr != nil {
			for i := len(locks) - 1; i >= 0; i-- {
				_ = locks[i]()
			}
			return nil, lockErr
		}
		locks = append(locks, unlock)
	}
	return locks, nil
}

func unlockRecipeScopes(locks []func() error) error {
	var first error
	for i := len(locks) - 1; i >= 0; i-- {
		if err := locks[i](); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ReadLock reads one scope lock under its shared coordination flock.
func ReadLock(s Scope) (Lock, error) {
	unlock, err := lockScopeMode(s, false)
	if err != nil {
		return Lock{}, err
	}
	lock, readErr := s.Lock()
	unlockErr := unlock()
	if readErr != nil {
		return Lock{}, readErr
	}
	return lock, unlockErr
}

func snapshotLocked() (RecipeSnapshot, error) {
	manifests, err := listManifestsLocked()
	if err != nil {
		return RecipeSnapshot{}, err
	}
	global, err := ScopeFor(true)
	if err != nil {
		return RecipeSnapshot{}, err
	}
	project, err := ScopeFor(false)
	if err != nil {
		return RecipeSnapshot{}, err
	}
	locks := map[string]Lock{}
	globalLock, err := global.Lock()
	if err != nil {
		return RecipeSnapshot{}, err
	}
	locks[global.Name] = globalLock
	if project.Name == "project" {
		projectLock, lockErr := project.Lock()
		if lockErr != nil {
			return RecipeSnapshot{}, lockErr
		}
		locks[project.Name] = projectLock
	}
	snapshot := RecipeSnapshot{
		Roots:     Roots(),
		Manifests: manifests,
		Scopes:    make(map[string]string, len(manifests)),
		Pins:      make(map[string]LockEntry, len(manifests)),
	}
	for _, manifest := range manifests {
		_, scope, found, resolveErr := resolvePath(manifest.Name)
		if resolveErr != nil {
			return RecipeSnapshot{}, resolveErr
		}
		if !found {
			return RecipeSnapshot{}, fmt.Errorf("recipe %q disappeared while reading its snapshot", manifest.Name)
		}
		snapshot.Scopes[manifest.Name] = scope
		if lock, ok := locks[scope]; ok {
			if pin, exists := lock.Recipes[manifest.Name]; exists {
				snapshot.Pins[manifest.Name] = pin
			}
		}
	}
	return snapshot, nil
}

// ListSnapshot reads visible manifests and pins under shared scope locks.
func ListSnapshot() (RecipeSnapshot, error) {
	locks, err := lockRecipeScopes(false)
	if err != nil {
		return RecipeSnapshot{}, err
	}
	snapshot, readErr := snapshotLocked()
	unlockErr := unlockRecipeScopes(locks)
	if readErr != nil {
		return RecipeSnapshot{}, readErr
	}
	return snapshot, unlockErr
}

// RepairSnapshot validates and repairs the project cache, then materializes a
// coherent view while retaining exclusive locks for the full read-may-repair.
func RepairSnapshot() (RecipeSnapshot, error) {
	locks, err := lockRecipeScopes(true)
	if err != nil {
		return RecipeSnapshot{}, err
	}
	project, err := ScopeFor(false)
	if err != nil {
		_ = unlockRecipeScopes(locks)
		return RecipeSnapshot{}, err
	}
	if project.Name == "project" {
		if err := repairProjectLocked(project); err != nil {
			_ = unlockRecipeScopes(locks)
			return RecipeSnapshot{}, err
		}
	}
	snapshot, readErr := snapshotLocked()
	unlockErr := unlockRecipeScopes(locks)
	if readErr != nil {
		return RecipeSnapshot{}, readErr
	}
	return snapshot, unlockErr
}
