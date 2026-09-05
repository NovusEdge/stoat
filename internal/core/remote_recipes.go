package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
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
// It reads vm.toml only. recipes.RemoveChecked calls it while holding the
// scope lock exclusively, and List resolves every VM's manifests through
// recipes.ManifestFor, which takes that same lock on a second descriptor and
// deadlocks the process.
func RecipeUsers(name string) ([]string, error) {
	vms, err := config.List()
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

// AddOpts carries what the CLI passes as flags. Yes is accepted and ignored
// here: recipes.Add asks nothing, and the preview prompt lives in the CLI.
type AddOpts struct {
	Ref    string
	Global bool
	Force  bool
	Yes    bool
}

// AddRecipe installs one recipe in the scope of the current directory. Ref
// pins a tag or branch; an empty Ref takes the source's default branch.
func AddRecipe(name string, opts AddOpts) error {
	s, err := recipes.ScopeFor(opts.Global)
	if err != nil {
		return err
	}
	ref := name
	if opts.Ref != "" {
		ref = name + "@" + opts.Ref
	}
	_, err = recipes.Add(s, ref, opts.Force)
	return err
}

// UpdateRecipe repins one recipe, or every remote recipe when name is empty.
func UpdateRecipe(name string) error {
	s, err := recipes.ScopeFor(false)
	if err != nil {
		return err
	}
	var names []string
	if name != "" {
		names = []string{name}
	}
	_, err = recipes.Update(s, names)
	return err
}

// RemoveRecipe deletes one recipe. Without force it refuses while a VM lists
// it; recipes.RemoveChecked runs that check while holding the scope lock, so
// a VM cannot start using the recipe between the check and the removal.
func RemoveRecipe(name string, force bool) error {
	s, err := recipes.ScopeFor(false)
	if err != nil {
		return err
	}
	var users func() ([]string, error)
	if !force {
		users = func() ([]string, error) { return RecipeUsers(name) }
	}
	if err := recipes.RemoveChecked(s, name, users); err != nil {
		var inUse *recipes.RemoveInUse
		if errors.As(err, &inUse) {
			return fmt.Errorf("%w: %s is used by %s", ErrInUse, name, strings.Join(inUse.Users, ", "))
		}
		return err
	}
	return nil
}
