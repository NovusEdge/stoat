package project

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/tomlx"
)

// SecretsPath is the project's secret store. It is gitignored with the rest
// of CacheDir, and stoat writes it 0600.
func SecretsPath(dir string) string {
	return filepath.Join(dir, CacheDir, "secrets.toml")
}

// Secrets returns one declaration's secrets as recipe to param to value.
//
// The file is keyed <vm key>.<recipe>.<param>, so one file covers every VM in
// the project. An absent file is not an error: a project whose recipes need
// no secret never has one.
func (p *Project) Secrets(key string) (map[string]map[string]string, error) {
	path := SecretsPath(p.Dir)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	// A secret readable by every account on the host is not a secret. The
	// refusal names the mode so the fix is one chmod.
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return nil, fmt.Errorf("secrets.toml: mode %#o, want 0600", mode)
	}

	var all map[string]map[string]map[string]string
	if err := tomlx.Decode(path, &all, tomlx.Reject); err != nil {
		return nil, fmt.Errorf("secrets.toml: %w", err)
	}
	if all[key] == nil {
		return map[string]map[string]string{}, nil
	}
	return all[key], nil
}
