package core

import (
	"errors"
	"fmt"
	"sort"

	"github.com/novusedge/stoat/internal/recipes"
)

// ErrLockOutOfDate identifies a project declaration that is not pinned.
var ErrLockOutOfDate = errors.New("stoat.lock is out of date")

// SyncRecipes validates the current project pin and repairs its cache only
// when a lock entry is missing or no longer matches the active checkout.
// Global recipe state is not touched by an apply in a non-project directory.
func SyncRecipes() error {
	err := recipes.SyncProject()
	if errors.Is(err, recipes.ErrLockOutOfDate) {
		return fmt.Errorf("%w; run stoat recipe lock", ErrLockOutOfDate)
	}
	return err
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
