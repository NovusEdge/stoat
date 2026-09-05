package project

import (
	"errors"
	"path/filepath"
)

// SecretsPath is the project's secret store.
func SecretsPath(dir string) string {
	return filepath.Join(dir, CacheDir, "secrets.toml")
}

// Secrets returns one declaration's secrets as recipe to param to value.
func (p *Project) Secrets(key string) (map[string]map[string]string, error) {
	return nil, errors.New("project: not implemented")
}
