package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/tomlx"
)

// Manifest is a recipe.toml, the v2 recipe format
// (docs/recipe-spec-v2.md): a directory holding one manifest and one or
// more shell scripts, replacing the old flat "<name>.<os>.sh" files.
type Manifest struct {
	Schema      int                 `toml:"schema"`
	Name        string              `toml:"name"`
	Description string              `toml:"description"`
	Version     string              `toml:"version"`
	OS          []string            `toml:"os"`
	Requires    []string            `toml:"requires"`
	Stage       string              `toml:"stage"` // "install" | "provision"
	Script      string              `toml:"script"`
	Scripts     map[string]string   `toml:"scripts"` // OS-specific overrides
	Auto        bool                `toml:"auto"`
	Run         string              `toml:"run"`     // "once" | "always" | "manual"
	Reboot      bool                `toml:"reboot"`  // guest needs a reboot after this recipe to take effect
	Runtime     string              `toml:"runtime"` // "sh" | "python3", the interpreter the script runs under
	Depends     []string            `toml:"depends"` // recipe names that must run before this one
	ParamsRaw   map[string]rawParam `toml:"params"`
	Params      map[string]Param    `toml:"-"`
	Outputs     map[string]string   `toml:"outputs"`
	Health      Health              `toml:"health"`

	dir string // recipe directory, set by ParseManifest; scripts resolve against it
}

// Param is one declared input of a schema-3 recipe. Default is the spelling
// the recipe receives in STOAT_PARAM_<NAME>, regardless of its type.
type Param struct {
	Name     string
	Type     string
	Default  string
	Help     string
	Required bool
	Values   []string
}

// rawParam is the TOML representation of a parameter. TOML preserves the
// literal type of default, so ParseManifest renders it after decoding.
type rawParam struct {
	Type     string   `toml:"type"`
	Default  any      `toml:"default"`
	Help     string   `toml:"help"`
	Required bool     `toml:"required"`
	Values   []string `toml:"values"`
}

// Output is one declared result of a recipe.
type Output struct {
	Name string
	Help string
}

// Health is a recipe's health-check command. An empty Check means no check.
type Health struct {
	Check   string `toml:"check"`
	Timeout string `toml:"timeout"`
}

// DefaultHealthTimeout is used when a health check omits timeout.
const DefaultHealthTimeout = 30 * time.Second

// Duration returns the configured health timeout. Invalid values fall back to
// the default because ParseManifest rejects them before a manifest is used.
func (h Health) Duration() time.Duration {
	if h.Check == "" {
		return 0
	}
	if h.Timeout == "" {
		return DefaultHealthTimeout
	}
	d, err := time.ParseDuration(h.Timeout)
	if err != nil || d <= 0 {
		return DefaultHealthTimeout
	}
	return d
}

// SecretNames returns the names of secret parameters.
func (m Manifest) SecretNames() []string {
	var names []string
	for _, p := range m.SortedParams() {
		if p.Type == "secret" {
			names = append(names, p.Name)
		}
	}
	return names
}

// SortedParams returns declared parameters in name order.
func (m Manifest) SortedParams() []Param {
	params := make([]Param, 0, len(m.Params))
	for _, p := range m.Params {
		params = append(params, p)
	}
	sort.Slice(params, func(i, j int) bool { return params[i].Name < params[j].Name })
	return params
}

// SortedOutputs returns declared outputs in name order.
func (m Manifest) SortedOutputs() []Output {
	outputs := make([]Output, 0, len(m.Outputs))
	for name, help := range m.Outputs {
		outputs = append(outputs, Output{Name: name, Help: help})
	}
	sort.Slice(outputs, func(i, j int) bool { return outputs[i].Name < outputs[j].Name })
	return outputs
}

var validStages = map[string]bool{"install": true, "provision": true}

var validRuns = map[string]bool{"once": true, "always": true, "manual": true}

var validRuntimes = map[string]bool{"sh": true, "python3": true}

var paramName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var validParamTypes = []string{"string", "int", "bool", "enum", "secret"}

// ParseManifest reads and validates a recipe.toml at path. Defaults are
// applied before validation: Stage defaults to "provision" (the common
// case, docs/recipe-spec-v2.md's Stages section), Run defaults to "once",
// Runtime to "sh", and Schema to 2 for older manifests.
func ParseManifest(path string) (Manifest, error) {
	var m Manifest
	if err := tomlx.Decode(path, &m, tomlx.Reject); err != nil {
		return Manifest{}, err
	}
	m.dir = filepath.Dir(path)

	if m.Schema == 0 {
		m.Schema = 2
	}
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
	if m.Schema > 3 {
		return Manifest{}, fmt.Errorf("%s: schema %d is newer than this stoat (3)", path, m.Schema)
	}
	if m.Schema < 3 && (len(m.ParamsRaw) > 0 || len(m.Outputs) > 0 || m.Health.Check != "" || m.Health.Timeout != "") {
		return Manifest{}, fmt.Errorf("%s: params, outputs and health require schema 3", path)
	}
	if err := m.buildParams(); err != nil {
		return Manifest{}, err
	}
	if err := validateHealth(path, m.Health); err != nil {
		return Manifest{}, err
	}

	return m, nil
}

// buildParams turns raw TOML declarations into the normalized parameter map.
func (m *Manifest) buildParams() error {
	m.Params = make(map[string]Param, len(m.ParamsRaw))
	for name, raw := range m.ParamsRaw {
		if !paramName.MatchString(name) {
			return fmt.Errorf("%s: param %q must match [a-z][a-z0-9_]*", m.Name, name)
		}
		if !containsString(validParamTypes, raw.Type) {
			return fmt.Errorf("%s.%s: type %q is not one of %s", m.Name, name, raw.Type, strings.Join(validParamTypes, ", "))
		}
		def, err := renderDefault(raw.Type, raw.Default)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", m.Name, name, err)
		}
		if raw.Type == "secret" && raw.Default != nil {
			return fmt.Errorf("%s.%s: a secret has no default", m.Name, name)
		}
		if raw.Type == "enum" {
			if len(raw.Values) == 0 {
				return fmt.Errorf("%s.%s: an enum needs values", m.Name, name)
			}
			if raw.Default != nil && !containsString(raw.Values, def) {
				return fmt.Errorf("%s.%s: %q is not one of %s", m.Name, name, def, strings.Join(raw.Values, ", "))
			}
		}
		if raw.Default == nil && !raw.Required {
			return fmt.Errorf("%s.%s: needs a default or required = true", m.Name, name)
		}
		m.Params[name] = Param{
			Name: name, Type: raw.Type, Default: def, Help: raw.Help,
			Required: raw.Required, Values: raw.Values,
		}
	}
	return nil
}

// renderDefault converts TOML's typed literal into the guest string form.
func renderDefault(typ string, v any) (string, error) {
	if v == nil {
		return "", nil
	}
	switch typ {
	case "int":
		switch n := v.(type) {
		case int64:
			return strconv.FormatInt(n, 10), nil
		case int:
			return strconv.Itoa(n), nil
		default:
			return "", fmt.Errorf("default %v is not an integer", v)
		}
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("default %v is not a boolean", v)
		}
		return strconv.FormatBool(b), nil
	default:
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("default %v is not a string", v)
		}
		return s, nil
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateHealth(path string, h Health) error {
	if h.Check == "" || h.Timeout == "" {
		return nil
	}
	d, err := time.ParseDuration(h.Timeout)
	if err != nil || d <= 0 {
		return fmt.Errorf("%s: health.timeout %q is not a positive duration", path, h.Timeout)
	}
	return nil
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
