package core

import (
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

// TestNeedsProvisionNothingApplied: a fresh VM has a recipe that has never
// run, so provisioning it does real work.
func TestNeedsProvisionNothingApplied(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	v := &config.VM{Mode: "live", OS: "alpine", Recipes: []string{"tool"}}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("got false, want true: the recipe has never run")
	}
}

// TestNeedsProvisionAllAppliedNoShare: every recipe already ran at its
// current script hash and there is no share to mount, so nothing is left
// for a provision run to do.
func TestNeedsProvisionAllAppliedNoShare(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Mode: "live", OS: "alpine", Recipes: []string{"tool"},
		Applied: map[string]config.AppliedRecipe{"tool": {Version: "1.0", Hash: hash}},
	}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("got true, want false: the recipe already ran and there is no share")
	}
}

// TestNeedsProvisionChangedRecipe pins the case filterByRunMode was extended
// for: a script fixed after it was applied must run again.
func TestNeedsProvisionChangedRecipe(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho fixed\n")
	v := &config.VM{
		Mode: "live", OS: "alpine", Recipes: []string{"tool"},
		Applied: map[string]config.AppliedRecipe{"tool": {Version: "1.0", Hash: "stale"}},
	}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("got false, want true: the script hash no longer matches")
	}
}

// TestNeedsProvisionDiskWithShareEvenWhenApplied: a disk VM's share mount is
// idempotent but not tracked in Applied, so it always counts as work.
func TestNeedsProvisionDiskWithShareEvenWhenApplied(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Mode: "disk", OS: "alpine", Recipes: []string{"tool"}, Share: "/host/path",
		Applied: map[string]config.AppliedRecipe{"tool": {Version: "1.0", Hash: hash}},
	}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("got false, want true: a disk VM with a share still needs the mount step")
	}
}

// TestNeedsProvisionCloudUnapplied: a cloud VM with recipes not yet in Applied
// returns true. On first boot this triggers a no-op pass (discoverCloudInitApplied
// finds the markers); for recipes added after creation it runs them.
func TestNeedsProvisionCloudUnapplied(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	v := &config.VM{Mode: "cloud", OS: "alpine", Recipes: []string{"tool"}}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Error("got false, want true: the recipe has never run (or markers not yet discovered)")
	}
}

// TestNeedsProvisionCloudAllApplied: a cloud VM with all recipes already in
// Applied (markers discovered, or added and run) returns false.
func TestNeedsProvisionCloudAllApplied(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho one\n")
	hash, err := recipes.ScriptHash("tool", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Mode: "cloud", OS: "alpine", Recipes: []string{"tool"},
		Applied: map[string]config.AppliedRecipe{"tool": {Version: "1.0", Hash: hash}},
	}

	got, err := NeedsProvision(v)
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Error("got true, want false: all recipes already applied")
	}
}
