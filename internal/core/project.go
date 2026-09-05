package core

import (
	"errors"

	"github.com/novusedge/stoat/internal/project"
)

// SpecFor turns one declaration into the Spec that Create takes.
func SpecFor(p *project.Project, key string) (Spec, error) {
	return Spec{}, errors.New("core: not implemented")
}

// Drift is one field where the declaration and vm.toml disagree.
//
// Key is the declaration key, not the global name: the user wrote "dev" and
// every message about it says "dev".
type Drift struct {
	Key   string
	Field string
	From  string
	To    string

	// NeedsRestart is true for a field qemu reads at start and cannot change
	// on a live guest: cpus, ram and shares. stoat up saves such a change and
	// reports it; it takes effect at the next down and up.
	NeedsRestart bool
}

// Diff compares one declaration to the VM it names.
func Diff(p *project.Project, key string) ([]Drift, error) {
	return nil, errors.New("core: not implemented")
}
