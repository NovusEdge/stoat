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

// Reconciled is what one Reconcile call did.
type Reconciled struct {
	Key  string
	Name string

	// Created is true when the VM did not exist and Reconcile made it.
	Created bool

	// Drift is what the declaration changed on an existing VM. It is empty on
	// a create: a VM built from the declaration cannot differ from it.
	Drift []Drift

	// RestartPending is true when at least one applied drift needs a restart.
	// The change is already in vm.toml; the guest sees it at the next down
	// and up.
	RestartPending bool
}

// Reconcile makes the VM named by one declaration match it.
func Reconcile(p *project.Project, key string) (Reconciled, error) {
	return Reconciled{}, errors.New("core: not implemented")
}
