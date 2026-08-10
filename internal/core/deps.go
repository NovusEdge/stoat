package core

import (
	"fmt"

	"github.com/novusedge/stoat/internal/recipes"
)

// loadManifests resolves each name to its manifest. A name with no recipe.toml
// errors; callers pass names recipes.List already vetted.
func loadManifests(names []string) (map[string]recipes.Manifest, error) {
	manifests := make(map[string]recipes.Manifest, len(names))
	for _, name := range names {
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: recipe %q has no recipe.toml", ErrRecipeNotApplicable, name)
		}
		manifests[name] = m
	}
	return manifests, nil
}

// CheckDependencies validates the dependency edges among names: every recipe's
// declared dependency must be present in names, and the set must have no cycle.
// It is the create-time and CLI check. Neither Create nor the CLI auto-adds a
// missing dependency; the caller lists it explicitly (docs/specs
// recipe-system-fixes §2). The TUI uses ResolveDependencies instead.
func CheckDependencies(names []string) error {
	manifests, err := loadManifests(names)
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(names))
	for _, n := range names {
		present[n] = true
	}
	for _, n := range names {
		for _, dep := range manifests[n].Depends {
			if !present[dep] {
				return fmt.Errorf("%w: %s depends on %s; add it to the recipe list", ErrRecipeNotApplicable, n, dep)
			}
		}
	}
	if _, err := recipes.TopoSort(names, manifests); err != nil {
		return fmt.Errorf("%w: %v", ErrRecipeNotApplicable, err)
	}
	return nil
}

// DepAddition is one dependency ResolveDependencies pulled in: the recipe to
// add, and the recipe whose depends list named it.
type DepAddition struct {
	Recipe     string
	RequiredBy string
}

// ResolveDependencies returns the recipes to add to names so every declared
// dependency is present, in add order. It follows depends edges transitively.
// Each added dependency must apply to osName (recipes.MatchesVM); one that does
// not errors with the reason. A cycle in the resulting set errors. The TUI
// auto-adds the returned recipes and reports each as "Added X (required by Y)".
func ResolveDependencies(osName string, names []string) ([]DepAddition, error) {
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	manifests := make(map[string]recipes.Manifest, len(names))
	var added []DepAddition

	queue := append([]string{}, names...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := manifests[name]; done {
			continue
		}
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: recipe %q has no recipe.toml", ErrRecipeNotApplicable, name)
		}
		manifests[name] = m

		for _, dep := range m.Depends {
			if !have[dep] {
				dm, ok, err := recipes.ManifestFor(dep)
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, fmt.Errorf("%w: %s depends on %s, which is not a recipe", ErrRecipeNotApplicable, name, dep)
				}
				if reason := recipes.MatchReason(&dm, osName); reason != "" {
					return nil, fmt.Errorf("%w: %s depends on %s: %s", ErrRecipeNotApplicable, name, dep, reason)
				}
				have[dep] = true
				added = append(added, DepAddition{Recipe: dep, RequiredBy: name})
			}
			queue = append(queue, dep)
		}
	}

	all := append([]string{}, names...)
	for _, a := range added {
		all = append(all, a.Recipe)
	}
	if _, err := recipes.TopoSort(all, manifests); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecipeNotApplicable, err)
	}
	return added, nil
}
