package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

var targetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// LoadTarget reads one VM's stored metadata without inspecting runtime state.
func LoadTarget(name string) (Target, error) {
	if !targetNameRE.MatchString(name) {
		return Target{}, fmt.Errorf("%w: VM name %q", core.ErrInvalidSpec, name)
	}
	path := filepath.Join(config.Root(), name, "vm.toml")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Target{}, fmt.Errorf("%w: %s", core.ErrNotFound, name)
		}
		return Target{}, fmt.Errorf("%w: %s: %v", core.ErrBroken, name, err)
	}
	v, err := config.Load(name)
	if err != nil {
		return Target{}, fmt.Errorf("%w: %s: %v", core.ErrBroken, name, err)
	}
	return Target{Name: name, Mode: v.Mode, AgentAccess: v.AgentAccess}, nil
}
