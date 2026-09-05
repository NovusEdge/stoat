// Package project owns stoat.toml: the VMs, recipes and params a repository
// declares. It reads the file and answers name questions about it. It runs
// nothing and touches no VM; internal/core does that.
package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/tomlx"
)

// FileName is the declaration file. Its presence in os.Getwd() is the whole
// project-scope test; see Find.
const FileName = "stoat.toml"

// CacheDir holds the recipe cache and the secrets file. It is gitignored by
// stoat init and by recipe add.
const CacheDir = ".stoat"

// Schema is the schema this stoat reads. A file declaring a higher one is
// refused, so a newer project does not decode as a silently smaller one.
const Schema = 1

// nameRE is the VM name grammar, shared by a declaration key, a name
// override and the generated global name. internal/mcpsrv's checkVMName uses
// the same expression.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// tableRE matches a [vms.<key>] header and nothing deeper. The closing
// bracket is what excludes [vms.dev.params.docker].
var tableRE = regexp.MustCompile(`(?m)^\s*\[vms\.([^.\]\s]+)\]\s*$`)

// VM is one [vms.<key>] declaration. Every field except Image is optional and
// falls back to the same default stoat new applies.
type VM struct {
	Key string `toml:"-"`

	Name        string                    `toml:"name"`
	Image       string                    `toml:"image"`
	CPUs        int                       `toml:"cpus"`
	RAM         int                       `toml:"ram"`
	Disk        string                    `toml:"disk"`
	Recipes     []string                  `toml:"recipes"`
	Shares      []string                  `toml:"shares"`
	AgentAccess string                    `toml:"agent_access"`
	Params      map[string]map[string]any `toml:"params"`
}

// meta is the [project] table.
type meta struct {
	Name string `toml:"name"`
}

// file is the decoded shape of stoat.toml. Recipes decodes as map[string]any
// on purpose: a value is either a ref string or an inline source table, and
// internal/recipes owns that union. Decoding it into a concrete type here
// would give this package a second, drifting definition of it.
type file struct {
	Schema  int            `toml:"schema"`
	Project meta           `toml:"project"`
	Recipes map[string]any `toml:"recipes"`
	VMs     map[string]VM  `toml:"vms"`
}

// Project is one loaded stoat.toml.
type Project struct {
	// Dir is the absolute directory the file was loaded from. It is the root
	// every shares entry must resolve inside.
	Dir  string
	Name string

	// VMs are the declarations in the order they appear in the file. Every
	// no-argument command iterates this slice, so the order is part of the
	// contract, not an implementation detail.
	VMs []VM

	// Recipes is the [recipes] table, uninterpreted. internal/recipes reads it.
	Recipes map[string]any

	byKey map[string]VM
}

// Load reads dir/stoat.toml.
func Load(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(abs, FileName)

	var f file
	if err := tomlx.Decode(path, &f, tomlx.Reject); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Schema > Schema {
		return nil, fmt.Errorf("%s: schema %d is newer than this stoat (%d)", FileName, f.Schema, Schema)
	}

	p := &Project{Dir: abs, Recipes: f.Recipes, byKey: make(map[string]VM, len(f.VMs))}
	p.Name = f.Project.Name
	if p.Name == "" {
		p.Name = slug(filepath.Base(abs))
	}
	if !nameRE.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: project.name %q must match %s", FileName, p.Name, nameRE)
	}

	for _, key := range order(path, f.VMs) {
		if !nameRE.MatchString(key) {
			return nil, fmt.Errorf("%s: vms.%s: a vm key must match %s", FileName, key, nameRE)
		}
		v := f.VMs[key]
		v.Key = key
		if v.Image == "" {
			return nil, fmt.Errorf("%s: vms.%s: image is required", FileName, key)
		}
		if v.Name != "" && !nameRE.MatchString(v.Name) {
			return nil, fmt.Errorf("%s: vms.%s.name %q must match %s", FileName, key, v.Name, nameRE)
		}
		p.VMs = append(p.VMs, v)
		p.byKey[key] = v
	}

	// The global name is the VM's directory under the data root, so two
	// declarations that resolve to one name would fight over one directory.
	seen := map[string]string{}
	for _, v := range p.VMs {
		g := p.GlobalName(v.Key)
		if first, dup := seen[g]; dup {
			a, b := sortPair(first, v.Key)
			return nil, fmt.Errorf("%s: vms.%s and vms.%s both resolve to %q", FileName, a, b, g)
		}
		seen[g] = v.Key
	}
	return p, nil
}

// Find loads the project in the current directory, if there is one. There is
// no walk-up: a command's scope must be readable from the directory the user
// typed it in, not from a parent three levels up.
func Find() (*Project, bool, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		return nil, false, nil
	}
	p, err := Load(dir)
	if err != nil {
		return nil, false, err
	}
	return p, true, nil
}

// GlobalName is the VM's directory name under the data root: the declaration's
// own name if it set one, otherwise "<project>-<key>".
func (p *Project) GlobalName(key string) string {
	v, ok := p.byKey[key]
	if !ok {
		return ""
	}
	if v.Name != "" {
		return v.Name
	}
	return p.Name + "-" + key
}

// Resolve turns a bare command argument into a global VM name. It tries the
// declaration key first, then a global name, so `stoat ssh dev` reaches
// myrepo-dev and `stoat ssh myrepo-dev` still works.
func (p *Project) Resolve(arg string) (string, bool) {
	if _, ok := p.byKey[arg]; ok {
		return p.GlobalName(arg), true
	}
	for _, v := range p.VMs {
		if g := p.GlobalName(v.Key); g == arg {
			return g, true
		}
	}
	return "", false
}

// VM returns one declaration by key.
func (p *Project) VM(key string) (VM, bool) {
	v, ok := p.byKey[key]
	return v, ok
}

// KeyFor is Resolve backwards: it names the declaration a global name belongs
// to, for a message that must say "dev" where the user wrote "dev".
func (p *Project) KeyFor(global string) (string, bool) {
	for _, v := range p.VMs {
		if p.GlobalName(v.Key) == global {
			return v.Key, true
		}
	}
	return "", false
}

// order returns the declaration keys in file order. tomlx decodes into a Go
// map, which has none, so the header positions in the raw file are the only
// record of it. A key the scan misses, because the file declares only a
// sub-table for it, is appended in sorted order.
func order(path string, vms map[string]VM) []string {
	var out []string
	seen := map[string]bool{}
	if data, err := os.ReadFile(path); err == nil {
		for _, m := range tableRE.FindAllStringSubmatch(string(data), -1) {
			key := strings.Trim(m[1], `"'`)
			if _, ok := vms[key]; ok && !seen[key] {
				out = append(out, key)
				seen[key] = true
			}
		}
	}
	var rest []string
	for key := range vms {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// slug lower-cases a directory name and replaces every character outside the
// name grammar with a dash, so a checkout called "MyRepo" or "my repo" still
// produces a usable default project name.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// sortPair orders two keys so the duplicate-name message reads the same on
// every run; a map walk would otherwise swap them between runs.
func sortPair(a, b string) (string, string) {
	if a < b {
		return a, b
	}
	return b, a
}
