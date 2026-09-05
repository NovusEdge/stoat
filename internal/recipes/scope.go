package recipes

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/project"
	"github.com/novusedge/stoat/internal/tomlx"
)

// ProjectFile is the name that puts stoat in project scope. internal/project
// owns it: the recipe scope and the VM scope must never disagree about which
// directory is a project.
const ProjectFile = project.FileName

// Scope is where one lock and its cache live.
//
// The project scope needs stoat.toml in the current directory. Stoat does not
// walk up: a recipe that appears because of a file three directories above
// the user is worse than one that does not appear at all.
type Scope struct {
	Name       string // "project" or "global"
	Dir        string // the directory holding the lock
	LockPath   string
	CachePath  string
	ConfigPath string // stoat.toml at project scope, empty at global scope
}

// Decl is one entry of stoat.toml's [recipes] table. An empty Source means the
// name resolves through the index.
type Decl struct {
	Source string
	Ref    string
}

// ScopeFor picks the scope a recipe command works in. global forces the home
// scope even inside a project.
func ScopeFor(global bool) (Scope, error) {
	if !global {
		p, ok, err := project.Find()
		if err != nil {
			return Scope{}, err
		}
		if ok {
			return Scope{
				Name:       "project",
				Dir:        p.Dir,
				LockPath:   filepath.Join(p.Dir, "stoat.lock"),
				CachePath:  filepath.Join(p.Dir, ".stoat", "recipes"),
				ConfigPath: filepath.Join(p.Dir, ProjectFile),
			}, nil
		}
	}
	root := config.Root()
	return Scope{
		Name:      "global",
		Dir:       root,
		LockPath:  filepath.Join(root, "stoat.lock"),
		CachePath: filepath.Join(root, "recipes"),
	}, nil
}

// Lock reads the scope's lock.
func (s Scope) Lock() (Lock, error) { return LoadLock(s.LockPath) }

// Save writes the scope's lock.
func (s Scope) Save(l Lock) error { return SaveLock(s.LockPath, l) }

// Decls reads stoat.toml's [recipes] table. The global scope has no
// declaration file, so it declares nothing.
//
// A value is a bare ref string or a table with source and ref, so it decodes
// into any and converts here. The whole document is decoded to preserve
// unrelated project tables when callers edit declarations.
func (s Scope) Decls() (map[string]Decl, error) {
	out := map[string]Decl{}
	if s.ConfigPath == "" {
		return out, nil
	}
	var f map[string]any
	if err := tomlx.Decode(s.ConfigPath, &f, tomlx.Warn(io.Discard)); err != nil {
		return nil, err
	}
	raw, ok := f["recipes"]
	if !ok {
		return out, nil
	}
	recipes, ok := stringMap(raw)
	if !ok {
		return nil, fmt.Errorf("%s: recipes must be a table", ProjectFile)
	}
	for name, v := range recipes {
		if err := validateRecipeName(name); err != nil {
			return nil, err
		}
		d, err := declFrom(name, v)
		if err != nil {
			return nil, err
		}
		out[name] = d
	}
	return out, nil
}

func declFrom(name string, v any) (Decl, error) {
	switch t := v.(type) {
	case string:
		return Decl{Ref: t}, nil
	default:
		m, ok := stringMap(t)
		if !ok {
			return Decl{}, fmt.Errorf("%s: recipes.%s must be a ref string or a table", ProjectFile, name)
		}
		d := Decl{}
		for key, value := range m {
			switch key {
			case "source":
				s, ok := value.(string)
				if !ok {
					return Decl{}, fmt.Errorf("%s: recipes.%s.source must be a string", ProjectFile, name)
				}
				d.Source = s
			case "ref":
				s, ok := value.(string)
				if !ok {
					return Decl{}, fmt.Errorf("%s: recipes.%s.ref must be a string", ProjectFile, name)
				}
				d.Ref = s
			default:
				return Decl{}, fmt.Errorf("%s: unknown key %q in recipes.%s", ProjectFile, key, name)
			}
		}
		return d, nil
	}
}

func stringMap(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok
}

// SetDecl writes one entry into stoat.toml's [recipes] table. The global scope
// has no declaration file, so it does nothing.
func (s Scope) SetDecl(name string, d Decl) error {
	if err := validateRecipeName(name); err != nil {
		return err
	}
	return s.editDecls(func(m map[string]any) {
		if d.Source == "" {
			m[name] = d.Ref
			return
		}
		m[name] = map[string]any{"source": d.Source, "ref": d.Ref}
	})
}

// RemoveDecl deletes one entry from stoat.toml's [recipes] table.
func (s Scope) RemoveDecl(name string) error {
	if err := validateRecipeName(name); err != nil {
		return err
	}
	return s.editDecls(func(m map[string]any) { delete(m, name) })
}

func (s Scope) editDecls(edit func(map[string]any)) error {
	if s.ConfigPath == "" {
		return nil
	}
	var f map[string]any
	if err := tomlx.Decode(s.ConfigPath, &f, tomlx.Warn(io.Discard)); err != nil {
		return err
	}
	var recipes map[string]any
	if raw, ok := f["recipes"]; ok {
		var converted bool
		recipes, converted = stringMap(raw)
		if !converted {
			return fmt.Errorf("%s: recipes must be a table", ProjectFile)
		}
	}
	if recipes == nil {
		recipes = map[string]any{}
	}
	edit(recipes)
	f["recipes"] = recipes
	return encodeProject(s.ConfigPath, f)
}

func encodeProject(path string, value any) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode, err := existingFileMode(path)
	if err != nil {
		return err
	}
	stageDir, err := os.MkdirTemp(dir, ".stoat-project-*")
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(stageDir, "stoat.toml")
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.RemoveAll(stageDir)
		return err
	}
	defer func() {
		if removeErr := os.RemoveAll(stageDir); removeErr != nil && err == nil {
			err = removeErr
		}
	}()
	if err := tomlx.Encode(tmpPath, value); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpPath, mode); err != nil {
			return err
		}
	}
	return os.Rename(tmpPath, path)
}

// IgnoreStoatDir appends ".stoat/" to dir's .gitignore. It does nothing
// outside a git checkout, and nothing when the line is already there.
func IgnoreStoatDir(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	path := filepath.Join(dir, ".gitignore")
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == ".stoat/" {
			return nil
		}
	}
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {
		body = append(body, '\n')
	}
	return os.WriteFile(path, append(body, ".stoat/\n"...), 0o644)
}
