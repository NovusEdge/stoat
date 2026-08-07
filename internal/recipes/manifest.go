package recipes

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Manifest is a recipe.toml, the v2 recipe format
// (docs/recipe-spec-v2.md): a directory holding one manifest and one or
// more shell scripts, replacing the old flat "<name>.<os>.sh" files.
type Manifest struct {
	Name        string            `toml:"name"`
	Description string            `toml:"description"`
	Version     string            `toml:"version"`
	OS          []string          `toml:"os"`
	Requires    []string          `toml:"requires"`
	Stage       string            `toml:"stage"` // "install" | "provision"
	Script      string            `toml:"script"`
	Scripts     map[string]string `toml:"scripts"` // OS-specific overrides
	Auto        bool              `toml:"auto"`
	Run         string            `toml:"run"` // "once" | "always" | "manual"

	dir string // recipe directory, set by ParseManifest; scripts resolve against it
}

var validStages = map[string]bool{"install": true, "provision": true}

var validRuns = map[string]bool{"once": true, "always": true, "manual": true}

// ParseManifest reads and validates a recipe.toml at path. Defaults are
// applied before validation: Stage defaults to "provision" (the common
// case, docs/recipe-spec-v2.md's Stages section), Run defaults to "once".
func ParseManifest(path string) (Manifest, error) {
	var m Manifest
	if _, err := toml.DecodeFile(path, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	m.dir = filepath.Dir(path)

	if m.Stage == "" {
		m.Stage = "provision"
	}
	if m.Run == "" {
		m.Run = "once"
	}

	if m.Name == "" {
		return Manifest{}, fmt.Errorf("%s: missing required field %q", path, "name")
	}
	if m.Script == "" {
		return Manifest{}, fmt.Errorf("%s: missing required field %q", path, "script")
	}
	if !validStages[m.Stage] {
		return Manifest{}, fmt.Errorf("%s: invalid stage %q, want %q or %q", path, m.Stage, "install", "provision")
	}
	if !validRuns[m.Run] {
		return Manifest{}, fmt.Errorf("%s: invalid run %q, want %q, %q, or %q", path, m.Run, "once", "always", "manual")
	}

	return m, nil
}

// ManifestFor resolves name (an entry in the recipes root, the same
// identifier VM.Recipes/ApplyOpts.Only use) to its recipe.toml manifest, v2's
// replacement for the old flat "<name>.<os>.sh" files (docs/recipe-spec-v2.md).
//
// ok is false with a nil error when name has no recipe.toml at all. That is
// not a failure: name is a v1 flat-file recipe, or an unrelated, nonexistent
// name that CheckRecipes/List already reject elsewhere. A caller uses ok to
// fall back to the old "always run it, no version tracking" behaviour,
// instead of treating absence as broken. A recipe.toml that exists but
// fails to parse is a real problem, and comes back as err instead.
func ManifestFor(name string) (m Manifest, ok bool, err error) {
	path := filepath.Join(dir(), name, "recipe.toml")
	if _, statErr := os.Stat(path); statErr != nil {
		return Manifest{}, false, nil
	}
	m, err = ParseManifest(path)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// ScriptFor returns the absolute path to the script osName runs: the
// Scripts override for osName if one is declared, otherwise the manifest's
// default Script.
func (m Manifest) ScriptFor(osName string) string {
	if s, ok := m.Scripts[osName]; ok {
		return filepath.Join(m.dir, s)
	}
	return filepath.Join(m.dir, m.Script)
}

// ScriptContent reads the script ScriptFor(osName) resolves to.
func (m Manifest) ScriptContent(osName string) (string, error) {
	b, err := os.ReadFile(m.ScriptFor(osName))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// capabilityOSes maps each recipe.toml "requires" capability to the guest
// OSes that have it (docs/recipe-spec-v2.md's Capabilities table). An
// unrecognised capability satisfies no OS, so MatchesVM rejects it rather
// than silently letting the recipe through.
var capabilityOSes = map[string][]string{
	"systemd": {"ubuntu", "debian", "arch", "fedora"},
	"openrc":  {"alpine"},
	"apt":     {"ubuntu", "debian"},
	"apk":     {"alpine"},
	"dnf":     {"fedora"},
	"pacman":  {"arch"},
}

// hasCapability reports whether cap resolves against vmOS.
func hasCapability(cap, vmOS string) bool {
	for _, o := range capabilityOSes[cap] {
		if o == vmOS {
			return true
		}
	}
	return false
}

// MatchesVM reports whether m's recipe is valid for a VM running vmOS: m.OS
// must either be empty (no restriction) or list vmOS, and every capability
// in m.Requires must resolve against vmOS per capabilityOSes.
func MatchesVM(m *Manifest, vmOS string) bool {
	if len(m.OS) > 0 {
		ok := false
		for _, o := range m.OS {
			if o == vmOS {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}

	for _, cap := range m.Requires {
		if !hasCapability(cap, vmOS) {
			return false
		}
	}

	return true
}
