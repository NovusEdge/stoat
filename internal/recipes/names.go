package recipes

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// validateRecipeName accepts safe filesystem components while preserving the
// names used by bundled and user-authored Go recipes.
func validateRecipeName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid recipe name %q", name)
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid recipe name %q", name)
	}
	for _, r := range name {
		if unicode.IsSpace(r) || r == 0 {
			return fmt.Errorf("invalid recipe name %q", name)
		}
	}
	return nil
}

func recipeTarget(root, name string) (string, error) {
	if err := validateRecipeName(name); err != nil {
		return "", err
	}
	return containedPath(root, name)
}

func containedPath(root, name string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, name)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("recipe %q escapes %s", name, root)
	}
	return target, nil
}
