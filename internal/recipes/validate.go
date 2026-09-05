package recipes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateTree checks a cloned recipe before it becomes an active cache
// entry. It validates the manifest and every script path the manifest names.
func ValidateTree(dir, name string) error {
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
			return fmt.Errorf("%s: no recipe.toml at the repository root", name)
		}
		return err
	}
	m, err := ParseManifest(manifestPath)
	if err != nil {
		return err
	}
	if m.Name != name {
		return fmt.Errorf("%s: recipe.toml is named %q", name, m.Name)
	}
	if err := validateScriptPath(root, m.Script); err != nil {
		return err
	}
	for osName, script := range m.Scripts {
		if err := validateScriptPath(root, script); err != nil {
			return fmt.Errorf("%s script %q: %w", osName, script, err)
		}
	}
	return validateTreeSymlinks(root)
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
	have, err := ScopeOf(name)
	if err != nil {
		return err
	}
	if have == "" {
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
