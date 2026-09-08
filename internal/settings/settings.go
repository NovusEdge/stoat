// Package settings owns config.toml: the account-wide values a VM record does
// not carry, such as which GCP project a gce VM lands in. It must not import
// internal/config; config imports this to resolve a VM's provider defaults.
package settings

import (
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/tomlx"
)

// GCE is the [providers.gce] table.
type GCE struct {
	Project                   string `toml:"project"`
	Zone                      string `toml:"zone"`
	ImpersonateServiceAccount string `toml:"impersonate_service_account"`

	// ServiceAccountKeyFile is the escape hatch for an environment that can
	// supply neither Application Default Credentials nor impersonation. It is
	// never a fallback: an empty value means ADC, and stoat does not go
	// looking for a key file.
	ServiceAccountKeyFile string `toml:"service_account_key_file"`
}

type Providers struct {
	GCE GCE `toml:"gce"`
}

type Settings struct {
	Providers Providers `toml:"providers"`
}

// Path is config.toml's location in the data root.
func Path() string {
	if r := os.Getenv("STOAT_HOME"); r != "" {
		return filepath.Join(r, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".stoat", "config.toml")
	}
	return filepath.Join(home, ".stoat", "config.toml")
}

// Load reads config.toml. An absent file yields a zero Settings and no error:
// a user who runs only local VMs never writes one.
func Load() (*Settings, error) {
	s := &Settings{}
	if _, err := os.Stat(Path()); os.IsNotExist(err) {
		return s, nil
	}
	if err := tomlx.Decode(Path(), s); err != nil {
		return nil, err
	}
	return s, nil
}
