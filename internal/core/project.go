package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
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

// ErrImmutableDeclaration is returned by Diff when a declaration changes
// image or disk. Neither can be applied to an existing VM: image decides the
// disk contents and disk is the size that disk was created at, so the only
// honest answer is to delete the VM and let stoat up build it again.
var ErrImmutableDeclaration = errors.New("immutable declaration field")

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

// Diff compares one declaration to the VM it names. A VM that does not exist
// yet is not drift; Reconcile creates it.
func Diff(p *project.Project, key string) ([]Drift, error) {
	spec, err := SpecFor(p, key)
	if err != nil {
		return nil, err
	}
	v, err := load(spec.Name)
	if err != nil {
		return nil, err
	}

	// vm.toml stores the resolved ISO path or base image, never a catalog
	// id. The declared image is compared against its resolved spelling.
	img, err := resolveImage(spec.Image)
	if err != nil {
		return nil, err
	}
	now := img.isoField()
	if img.backend == "cloudinit" {
		now = img.abs
	}
	if was := declaredImage(v); was != now {
		return nil, fmt.Errorf("%w: %s: image changed (%s -> %s); run stoat rm %s and stoat up",
			ErrImmutableDeclaration, key, imageName(was), img.id(), key)
	}
	declaredDisk := strings.TrimSpace(spec.Disk)
	if declaredDisk == "" {
		mode, err := modeFor(img.backend, "")
		if err != nil {
			return nil, err
		}
		if mode != "live" {
			declaredDisk = DefaultDisk
		}
	}
	if declaredDisk != v.Disk {
		return nil, fmt.Errorf("%w: %s: disk changed (%s -> %s); run stoat rm %s and stoat up",
			ErrImmutableDeclaration, key, v.Disk, declaredDisk, key)
	}

	var out []Drift
	add := func(field, from, to string, restart bool) {
		if from != to {
			out = append(out, Drift{Key: key, Field: field, From: from, To: to, NeedsRestart: restart})
		}
	}
	if spec.CPUs != 0 {
		add("cpus", strconv.Itoa(v.CPUs), strconv.Itoa(spec.CPUs), true)
	}
	if spec.RAM != 0 {
		add("ram", strconv.Itoa(v.RAM), strconv.Itoa(spec.RAM), true)
	}
	add("recipes", strings.Join(v.Recipes, ","), strings.Join(spec.Recipes, ","), false)
	add("shares", renderShares(v.Shares), renderShares(spec.Shares), true)
	add("params", renderParams(v.Params), renderParams(spec.Params), false)
	if spec.AgentAccess != "" {
		add("agent_access", v.AgentAccess, spec.AgentAccess, false)
	}
	return out, nil
}

// declaredImage names the image a VM was created from, whichever field holds
// it. A cloud VM records Base, every other mode records ISO.
func declaredImage(v *config.VM) string {
	if v.Base != "" {
		return v.Base
	}
	return v.ISO
}

// imageName renders a stored image field back to a catalog id, so an
// image-changed message names the image the way a user typed it in
// stoat.toml rather than the file it resolved to. A field with no catalog
// match (a BYO image, or an absolute Base path) is returned as recorded.
func imageName(field string) string {
	rel := strings.TrimPrefix(field, "isos/")
	prefixed := rel != field
	if !prefixed {
		// A cloudinit image's stored field is img.abs, an absolute path with
		// no "isos/" prefix (Diff and Create both set it that way). Match on
		// its basename so a cloud VM's image-changed error names the catalog
		// id too, not the resolved file path.
		rel = filepath.Base(field)
	}
	for _, e := range iso.Catalog() {
		if MatchLocal(e, []string{rel}) == rel {
			return e.ID
		}
	}
	if prefixed {
		return rel
	}
	return field
}

// renderShares is the comparable form of a share list: guest mountpoint and
// host path, in order. The mount tag is derived from position, so comparing
// it would report drift for a reordering that changes nothing in the guest.
func renderShares(ss []config.Share) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = s.Guest + "=" + s.Host
	}
	return strings.Join(parts, ",")
}

// renderParams is the comparable form of a param table: recipe.param=value,
// sorted, so a map walk cannot report drift where there is none. It takes
// the stored shape, map[string]map[string]string, which is what both
// config.VM and core.Spec carry after SpecFor renders the declaration's TOML
// values.
func renderParams(m map[string]map[string]string) string {
	var parts []string
	for recipe, params := range m {
		for name, val := range params {
			parts = append(parts, fmt.Sprintf("%s.%s=%s", recipe, name, val))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// removedParams finds recipe.param entries that vm.toml has and the
// declaration no longer states, for a recipe the declaration still applies,
// so Reconcile unsets them instead of leaving a stale value that Diff
// reports as drift on every later stoat up. A recipe missing from keep is
// excluded: Update clears a dropped recipe's params (and secrets) itself
// when Reconcile removes it from vm.toml's recipe list, and naming it here
// too would ask Update to resolve a recipe already gone from that list.
func removedParams(have, want map[string]map[string]string, keep []string) map[string][]string {
	kept := make(map[string]bool, len(keep))
	for _, r := range keep {
		kept[r] = true
	}
	var out map[string][]string
	for recipe, params := range have {
		if !kept[recipe] {
			continue
		}
		for name := range params {
			if _, ok := want[recipe][name]; ok {
				continue
			}
			if out == nil {
				out = make(map[string][]string)
			}
			out[recipe] = append(out[recipe], name)
		}
	}
	return out
}

// filterSecrets keeps only the recipes recipes still names. project.Secrets
// reads every recipe table under a VM's key in secrets.toml regardless of
// which recipes the declaration currently applies, so a recipe dropped from
// [vms.x].recipes still shows up in spec.Secrets. Handing that unfiltered to
// Update fails manifestForVM's lookup with ErrRecipeNotApplicable, because
// the Recipes patch has already removed the recipe from the VM's own list by
// the time stageParamEdits validates against it.
func filterSecrets(secrets config.Secrets, recipes []string) config.Secrets {
	kept := make(map[string]bool, len(recipes))
	for _, r := range recipes {
		kept[r] = true
	}
	out := config.Secrets{}
	for recipe, values := range secrets {
		if kept[recipe] {
			out[recipe] = values
		}
	}
	return out
}

// AttachKeys fills VM.Key from p for every VM p declares. A VM no declaration
// names keeps an empty Key, which is what tells ls it is a global VM.
func AttachKeys(vms []VM, p *project.Project) {
	if p == nil {
		return
	}
	for i := range vms {
		if key, ok := p.KeyFor(vms[i].Name); ok {
			vms[i].Key = key
		}
	}
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

// Reconcile makes the VM named by one declaration match it. A missing VM is
// created; an existing one takes every mutable difference through the same
// Update path stoat update uses, so there is one place that validates an
// edit. It does not start anything: the caller decides that.
func Reconcile(p *project.Project, key string) (Reconciled, error) {
	spec, err := SpecFor(p, key)
	if err != nil {
		return Reconciled{}, err
	}
	r := Reconciled{Key: key, Name: spec.Name}

	v, err := load(spec.Name)
	if errors.Is(err, ErrNotFound) {
		if _, err := Create(spec); err != nil {
			return Reconciled{}, err
		}
		r.Created = true
		return r, nil
	} else if err != nil {
		return Reconciled{}, err
	}

	drift, err := Diff(p, key)
	if err != nil {
		return Reconciled{}, err
	}
	r.Drift = drift

	secrets := filterSecrets(spec.Secrets, spec.Recipes)
	if len(drift) == 0 {
		stored, err := config.LoadSecrets(v.Dir)
		if err != nil {
			return Reconciled{}, err
		}
		if reflect.DeepEqual(secrets, stored) {
			return r, nil
		}
	}

	patch := Patch{}
	for _, d := range drift {
		if d.NeedsRestart {
			r.RestartPending = true
		}
		switch d.Field {
		case "cpus":
			cpus := spec.CPUs
			patch.CPUs = &cpus
		case "ram":
			ram := spec.RAM
			patch.RAM = &ram
		case "recipes":
			recipes := spec.Recipes
			patch.Recipes = &recipes
		case "shares":
			shares := spec.Shares
			patch.Shares = &shares
		case "params":
			patch.SetParams = spec.Params
			patch.UnsetParams = removedParams(v.Params, spec.Params, spec.Recipes)
		case "agent_access":
			access := spec.AgentAccess
			patch.AgentAccess = &access
		}
	}
	patch.Secrets = secrets
	if _, err := Update(spec.Name, patch); err != nil {
		return Reconciled{}, err
	}
	return r, nil
}
