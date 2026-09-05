package recipes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidTree is the sentinel for a cloned recipe tree the user cannot fix
// by retrying: a bad name, a missing manifest, a manifest that names the
// wrong recipe, or a script/symlink path that escapes the tree. wire.MapError
// routes it to invalid_spec, the CLI-layer code for "the request itself is
// malformed" (as opposed to a transient or environmental failure).
var ErrInvalidTree = errors.New("invalid recipe tree")

// ValidateTree checks a cloned recipe before it becomes an active cache
// entry. It validates the manifest and every script path the manifest names.
func ValidateTree(dir, name string) error {
	if err := validateRecipeName(name); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTree, err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(root, "recipe.toml")
	if _, err := os.Stat(manifestPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s: no recipe.toml at the repository root", ErrInvalidTree, name)
		}
		return err
	}
	m, err := ParseManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTree, err)
	}
	if m.Name != name {
		return fmt.Errorf("%w: %s: recipe.toml is named %q", ErrInvalidTree, name, m.Name)
	}
	if err := validateScriptPath(root, m.Script); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTree, err)
	}
	for osName, script := range m.Scripts {
		if err := validateScriptPath(root, script); err != nil {
			return fmt.Errorf("%w: %s script %q: %v", ErrInvalidTree, osName, script, err)
		}
	}
	if err := validateTreeSymlinks(root); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTree, err)
	}
	return nil
}

func validateScriptPath(root, script string) error {
	path := filepath.Join(root, filepath.FromSlash(script))
	if !within(root, path) {
		return fmt.Errorf("script %q points outside the recipe", script)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("script %q: %w", script, err)
	}
	if !within(root, resolved) {
		return fmt.Errorf("script %q points outside the recipe", script)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("script %q: %w", script, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("script %q is not a regular file", script)
	}
	return nil
}

func validateTreeSymlinks(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if !within(root, resolved) {
			return fmt.Errorf("%s: symlink points outside the recipe", filepath.Base(path))
		}
		return nil
	})
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CheckCollision rejects local, bundled, and same-scope remote recipes. A
// remote recipe in another scope may shadow the lower-priority entry.
func CheckCollision(name, scope string) error {
	if err := validateRecipeName(name); err != nil {
		return err
	}
	if scope == "global" || scope == "project" {
		target, err := ScopeFor(scope == "global")
		if err != nil {
			return err
		}
		if scope == "global" || target.Name == "project" {
			lock, err := target.Lock()
			if err != nil {
				return err
			}
			if _, ok := lock.Recipes[name]; ok {
				return fmt.Errorf("%q is already a %s remote recipe; pick another name or use --force", name, scope)
			}
		}
	}
	// Add already holds the target scope's exclusive lock. Resolve the
	// collision from that locked snapshot instead of nesting another flock.
	_, have, found, err := resolvePath(name)
	if err != nil {
		return err
	}
	if !found || have == "" {
		return nil
	}
	if have == "local" || have == "bundled" {
		return fmt.Errorf("%q is a %s recipe; pick another name or use --force", name, have)
	}
	if have == scope {
		return fmt.Errorf("%q is already a %s remote recipe; pick another name or use --force", name, scope)
	}
	return nil
}
