package core

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/project"
)

// SpecFor turns one declaration into the Spec that Create takes.
//
// It sets only what the declaration states. Every other field stays zero so
// plan() applies the same default stoat new would, which is what makes a
// minimal [vms.x] produce the VM a user gets by pressing enter through the
// form.
func SpecFor(p *project.Project, key string) (Spec, error) {
	d, ok := p.VM(key)
	if !ok {
		return Spec{}, fmt.Errorf("%w: no vms.%s in %s", ErrNotFound, key, project.FileName)
	}
	shares, err := p.Shares(key)
	if err != nil {
		return Spec{}, err
	}
	secrets, err := p.Secrets(key)
	if err != nil {
		return Spec{}, err
	}
	params, err := stringParams(key, d.Params)
	if err != nil {
		return Spec{}, err
	}
	return Spec{
		Name:        p.GlobalName(key),
		Image:       d.Image,
		RAM:         d.RAM,
		CPUs:        d.CPUs,
		Disk:        d.Disk,
		Recipes:     d.Recipes,
		AgentAccess: d.AgentAccess,
		Project:     p.Dir,
		Shares:      configShares(shares),
		Params:      params,
		Secrets:     config.Secrets(secrets),
	}, nil
}

// stringParams renders a declaration's TOML param values to the strings the
// guest reads. TOML gives "port = 2375" as an int64 and "tls = true" as a
// bool; vm.toml, the recipe hash and STOAT_PARAM_* all carry one spelling of
// each, the same one recipes.ParseManifest renders a manifest default to.
func stringParams(key string, in map[string]map[string]any) (map[string]map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]map[string]string, len(in))
	for recipe, params := range in {
		vals := make(map[string]string, len(params))
		for name, v := range params {
			switch t := v.(type) {
			case string:
				vals[name] = t
			case int64:
				vals[name] = strconv.FormatInt(t, 10)
			case bool:
				vals[name] = strconv.FormatBool(t)
			default:
				return nil, fmt.Errorf("%w: %s: vms.%s.params.%s.%s: %v is not a string, an integer or a boolean",
					ErrInvalidSpec, project.FileName, key, recipe, name, v)
			}
		}
		out[recipe] = vals
	}
	return out, nil
}

// configShares converts project.Share to the on-disk shape. config cannot
// import project: project is a caller-side concept and config is the file
// format, so the conversion lives here, in the package that knows both.
func configShares(in []project.Share) []config.Share {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.Share, len(in))
	for i, s := range in {
		out[i] = config.Share{Tag: s.Tag, Host: s.Host, Guest: s.Guest}
	}
	return out
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
