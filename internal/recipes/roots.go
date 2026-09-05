package recipes

import (
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
)

// Root is one directory searched for recipes, with the scope a name found
// there reports.
type Root struct {
	Path  string
	Scope string // "project", "global", "local" or "bundled"
}

// Roots lists every recipe directory in shadow order: the project cache first,
// then the home directory three times, once per label it can carry.
//
// The global cache and the bundled set share ~/.stoat/recipes, so the label
// comes from the bookkeeping beside them rather than from the path: the home
// lock names the remote recipes and .manifest names stoat's own copies.
func Roots() []Root {
	var roots []Root
	if s, err := ScopeFor(false); err == nil && s.Name == "project" {
		roots = append(roots, Root{Path: s.CachePath, Scope: "project"})
	}
	home := dir()
	return append(roots,
		Root{Path: home, Scope: "global"},
		Root{Path: home, Scope: "local"},
		Root{Path: home, Scope: "bundled"},
	)
}

// ResolvePath finds name's recipe directory and the scope it belongs to. The
// first root that both holds the directory and owns the name wins.
func ResolvePath(name string) (path, scope string, ok bool, err error) {
	path, scope, ok, _ = resolvePath(name)
	return path, scope, ok, nil
}

func resolvePath(name string) (path, scope string, ok bool, err error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", "", false, nil
	}
	for _, root := range Roots() {
		d := filepath.Join(root.Path, name)
		if _, statErr := os.Stat(filepath.Join(d, "recipe.toml")); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return "", "", false, statErr
		}
		owned, ownsErr := owns(root, name)
		if ownsErr != nil {
			return "", "", false, ownsErr
		}
		if owned {
			return d, root.Scope, true, nil
		}
	}
	return "", "", false, nil
}

// ScopeOf returns name's scope, or "" when no root holds it.
func ScopeOf(name string) (string, error) {
	_, scope, _, _ := resolvePath(name)
	return scope, nil
}

// owns reports whether a name found under root carries root's label.
func owns(root Root, name string) (bool, error) {
	switch root.Scope {
	case "project":
		return true, nil
	case "global":
		return homeLockHas(name)
	case "bundled":
		return bundledHas(name), nil
	default: // local
		remote, err := homeLockHas(name)
		if err != nil {
			return false, err
		}
		return !remote && !bundledHas(name), nil
	}
}

// homeLockHas reports whether the home lock pins name.
func homeLockHas(name string) (bool, error) {
	lock, err := LoadLock(filepath.Join(config.Root(), "stoat.lock"))
	if err != nil {
		return false, err
	}
	_, ok := lock.Recipes[name]
	return ok, nil
}

// bundledHas reports whether stoat wrote name from the embedded set. The
// manifest is keyed by the path relative to the recipes root.
func bundledHas(name string) bool {
	_, ok := readManifest()[name+"/recipe.toml"]
	return ok
}
