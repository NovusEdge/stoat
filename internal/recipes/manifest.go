package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/tomlx"
)

// Manifest is a recipe.toml, the v2 recipe format
// (docs/recipe-spec-v2.md): a directory holding one manifest and one or
// more shell scripts, replacing the old flat "<name>.<os>.sh" files.
type Manifest struct {
	Schema      int               `toml:"schema"`
	Name        string            `toml:"name"`
	Description string            `toml:"description"`
	Version     string            `toml:"version"`
	OS          []string          `toml:"os"`
	Requires    []string          `toml:"requires"`
	Stage       string            `toml:"stage"` // "install" | "provision"
	Script      string            `toml:"script"`
	Scripts     map[string]string `toml:"scripts"` // OS-specific overrides
	Auto        bool              `toml:"auto"`
	Run         string            `toml:"run"`     // "once" | "always" | "manual"
	Reboot      bool              `toml:"reboot"`  // guest needs a reboot after this recipe to take effect
	Runtime     string            `toml:"runtime"` // "sh" | "python3", the interpreter the script runs under
	Depends     []string          `toml:"depends"` // recipe names that must run before this one
	Params      map[string]Param  `toml:"params"`
	Outputs     map[string]string `toml:"outputs"`
	Health      Health            `toml:"health"`

	dir string // recipe directory, set by ParseManifest; scripts resolve against it
}

// Param is one declared input of a schema-3 recipe.
type Param struct {
	Name     string   `toml:"name"`
	Type     string   `toml:"type"`
	Default  string   `toml:"default"`
	Help     string   `toml:"help"`
	Required bool     `toml:"required"`
	Values   []string `toml:"values"`
}

// Output is one declared result of a recipe.
type Output struct {
	Name string
	Help string
}

// Health is a recipe's health-check command.
type Health struct {
	Check   string `toml:"check"`
	Timeout string `toml:"timeout"`
}

// Duration returns the configured health timeout.
func (h Health) Duration() time.Duration { return 0 }

// SecretNames returns the names of secret parameters.
func (m Manifest) SecretNames() []string { return nil }

// SortedParams returns declared parameters in name order.
func (m Manifest) SortedParams() []Param { return nil }

// SortedOutputs returns declared outputs in name order.
func (m Manifest) SortedOutputs() []Output { return nil }

var validStages = map[string]bool{"install": true, "provision": true}

var validRuns = map[string]bool{"once": true, "always": true, "manual": true}

var validRuntimes = map[string]bool{"sh": true, "python3": true}

// ParseManifest reads and validates a recipe.toml at path. Defaults are
// applied before validation: Stage defaults to "provision" (the common
// case, docs/recipe-spec-v2.md's Stages section), Run defaults to "once".
func ParseManifest(path string) (Manifest, error) {
	var m Manifest
	if err := tomlx.Decode(path, &m, tomlx.Reject); err != nil {
		return Manifest{}, err
	}
	m.dir = filepath.Dir(path)

	if m.Stage == "" {
		m.Stage = "provision"
	}
	if m.Run == "" {
		m.Run = "once"
	}
	if m.Runtime == "" {
		m.Runtime = "sh"
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
	if !validRuntimes[m.Runtime] {
		return Manifest{}, fmt.Errorf("%s: invalid runtime %q, want %q or %q", path, m.Runtime, "sh", "python3")
	}

	return m, nil
}

// ManifestFor resolves name (an entry in the recipes root, the same
// identifier VM.Recipes/ApplyOpts.Only use) to its recipe.toml manifest
// (docs/recipe-spec-v2.md).
//
// ok is false with a nil error when name has no recipe.toml at all: an
// unrelated or nonexistent name that CheckRecipes/List reject elsewhere. A
// caller decides what absence means for it. A recipe.toml that exists but
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
// Scripts override for osName, then for each of the guest's aliases in
// order, then the manifest's default Script.
func (m Manifest) ScriptFor(osName string) string {
	keys := []string{osName}
	if o, ok := guest.Lookup(osName); ok {
		keys = append(keys, o.Aliases...)
	}
	for _, k := range keys {
		if s, ok := m.Scripts[k]; ok {
			return filepath.Join(m.dir, s)
		}
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

// hasCapability reports whether cap resolves against vmOS. The table comes
// from the loaded guests, so a new guest file adds capabilities without a
// Go edit.
func hasCapability(cap, vmOS string) bool {
	for _, o := range guest.Capabilities()[cap] {
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
	return MatchReason(m, vmOS) == ""
}

// MatchReason explains why m's recipe does not apply to a VM running vmOS, or
// returns "" if it does. It is the reason-string form of MatchesVM: the OS
// restriction is checked first, then each capability in Requires in order,
// stopping at the first failure. CheckRecipes turns the returned reason into
// the message a caller reads.
func MatchReason(m *Manifest, vmOS string) string {
	if len(m.OS) > 0 {
		ok := false
		for _, o := range m.OS {
			if o == vmOS {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("built for %s, not %s", strings.Join(m.OS, ", "), vmOS)
		}
	}

	for _, cap := range m.Requires {
		if !hasCapability(cap, vmOS) {
			return fmt.Sprintf("requires %s, which %s does not have", cap, vmOS)
		}
	}

	return ""
}
