package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/novusedge/stoat/internal/tomlx"
)

// SecretsName is the file holding secret recipe parameter values.
const SecretsName = "secrets.toml"

// Secrets maps a recipe name to its secret parameter values.
type Secrets map[string]map[string]string

// SecretsPath returns the path to this VM's secrets file.
func (v *VM) SecretsPath() string { return filepath.Join(v.Dir, SecretsName) }

// LoadSecrets reads one VM's secret parameter values.
func LoadSecrets(dir string) (Secrets, error) {
	path := filepath.Join(dir, SecretsName)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Secrets{}, nil
	}
	if err != nil {
		return nil, err
	}
	if perm := info.Mode().Perm(); perm&^0o600 != 0 {
		return nil, fmt.Errorf("%s: mode %#o, want 0600", SecretsName, perm)
	}
	secrets := Secrets{}
	if err := tomlx.Decode(path, &secrets, tomlx.Reject); err != nil {
		return nil, err
	}
	return secrets, nil
}

// SaveSecrets writes one VM's secret parameter values.
func SaveSecrets(dir string, s Secrets) error {
	path := filepath.Join(dir, SecretsName)
	clean := make(Secrets, len(s))
	for recipe, values := range s {
		if len(values) == 0 {
			continue
		}
		copyValues := make(map[string]string, len(values))
		for name, value := range values {
			copyValues[name] = value
		}
		clean[recipe] = copyValues
	}
	if len(clean) == 0 {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	tmp, err := os.CreateTemp(dir, ".secrets-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := tomlx.Encode(tmpPath, clean); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

// Names returns the names of a recipe's set secret parameters.
func (s Secrets) Names(recipe string) []string {
	var names []string
	for name, value := range s[recipe] {
		if value != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
