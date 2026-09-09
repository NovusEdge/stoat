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

	// MaxRunDuration bounds how long an instance runs before GCP stops it,
	// as a Go duration string. Empty means 24h.
	//
	// This is the backstop that survives stoat being uninstalled, so it is
	// set once at create and never moved: instances.setScheduling needs a
	// stopped instance. Extending a deadline moves the label instead.
	MaxRunDuration string `toml:"max_run_duration"`

	// SourceRange is the CIDR the SSH firewall rule admits. Empty means ask
	// an echo service what address it saw and scope the rule to that.
	//
	// That lookup is wrong for anyone whose SSH traffic leaves by a different
	// path than an HTTPS request: a split-tunnel VPN, a proxy, an outbound
	// NAT pool wide enough that the answer is one address among many. The
	// failure is a lockout rather than exposure, since the guest accepts keys
	// only, and this field is the way out of it.
	SourceRange string `toml:"source_range"`
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
