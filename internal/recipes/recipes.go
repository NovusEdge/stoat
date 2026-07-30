// Package recipes manages the shell scripts stoat runs inside guests.
package recipes

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

//go:embed *.sh
var bundled embed.FS

func dir() string { return filepath.Join(config.Root(), "recipes") }

// Path is the on-disk location of a recipe.
func Path(name string) string { return filepath.Join(dir(), name+".sh") }

// Install copies bundled recipes into the data root. Existing files are never
// overwritten, so local edits survive upgrades.
func Install() error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	items, err := bundled.ReadDir(".")
	if err != nil {
		return err
	}
	for _, it := range items {
		dst := filepath.Join(dir(), it.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		b, err := bundled.ReadFile(it.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// List returns installed recipe names, without the .sh suffix.
func List() ([]string, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sh") {
			out = append(out, strings.TrimSuffix(e.Name(), ".sh"))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Read returns a recipe's script body.
func Read(name string) (string, error) {
	b, err := os.ReadFile(Path(name))
	return string(b), err
}
