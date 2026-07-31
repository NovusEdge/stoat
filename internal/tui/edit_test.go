package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func editFixture(t *testing.T) editModel {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	return newEdit(&config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl",
		RAM: 4096, CPUs: 4, Disk: "8G", Share: "~/vms", SSHPort: 2200,
		Dir: t.TempDir(),
	})
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"8G", 8 << 30, true},
		{"512M", 512 << 20, true},
		{"1T", 1 << 40, true},
		{"2048", 2048, true}, // bare bytes, as qemu-img accepts
		{"1.5G", 1536 << 20, true},
		{"", 0, false},
		{"big", 0, false},
		{"0G", 0, false},
		{"-4G", 0, false},
	}
	for _, c := range cases {
		got, err := parseSize(c.in)
		if c.ok && err != nil {
			t.Errorf("parseSize(%q) errored: %v", c.in, err)
			continue
		}
		if !c.ok {
			if err == nil {
				t.Errorf("parseSize(%q) = %d, want an error", c.in, got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("parseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestEditRefusesDiskShrink is the guard that matters most. qemu-img will
// shrink a qcow2 without complaint, truncating the filesystem inside and
// destroying whatever lived in the tail. Exposing disk size in the UI is only
// safe because this refuses.
func TestEditRefusesDiskShrink(t *testing.T) {
	e := editFixture(t)
	e.inputs[eDisk].SetValue("4G") // was 8G

	_, err := e.validate(nil)
	if err == nil {
		t.Fatal("shrinking the disk was accepted; that silently destroys data")
	}
	if !strings.Contains(err.Error(), "grow") {
		t.Errorf("error = %q, should explain that disks only grow", err)
	}

	// Growing is fine, and is reported so the caller knows to run qemu-img.
	e.inputs[eDisk].SetValue("16G")
	a, err := e.validate(nil)
	if err != nil {
		t.Fatalf("growing the disk was refused: %v", err)
	}
	if a.resizeTo != "16G" {
		t.Errorf("resizeTo = %q, want 16G", a.resizeTo)
	}
	if a.vm.Disk != "16G" {
		t.Errorf("vm.Disk = %q, want 16G", a.vm.Disk)
	}
}

// TestEditRefusesPortCollision covers the other way this form can break a
// working VM: two VMs forwarding the same host port means the second one
// silently fails to start.
func TestEditRefusesPortCollision(t *testing.T) {
	e := editFixture(t)
	others := []*config.VM{
		{Name: "work", SSHPort: 2200},
		{Name: "other", SSHPort: 2201},
	}

	e.inputs[eSSHPort].SetValue("2201")
	if _, err := e.validate(others); err == nil {
		t.Fatal("a colliding ssh port was accepted")
	} else if !strings.Contains(err.Error(), "other") {
		t.Errorf("error = %q, should name the VM holding the port", err)
	}

	// Keeping its own port is not a collision with itself.
	e.inputs[eSSHPort].SetValue("2200")
	if _, err := e.validate(others); err != nil {
		t.Errorf("keeping the VM's own port was rejected: %v", err)
	}

	// Out-of-range ports are refused before any collision check.
	for _, bad := range []string{"80", "0", "99999", "http"} {
		e.inputs[eSSHPort].SetValue(bad)
		if _, err := e.validate(others); err == nil {
			t.Errorf("port %q was accepted", bad)
		}
	}
}

// TestEditRefusesCloudWithoutBase covers the mode switch. A cloud VM boots an
// overlay of a downloaded base image; without one there is nothing to boot,
// so flipping the mode would produce a VM that fails at start with a far less
// obvious message than this one.
func TestEditRefusesCloudWithoutBase(t *testing.T) {
	e := editFixture(t) // disk-mode Alpine, no Base
	e.mode = "cloud"

	_, err := e.validate(nil)
	if err == nil {
		t.Fatal("cloud mode was accepted for a VM with no base image")
	}
	if !strings.Contains(err.Error(), "base image") {
		t.Errorf("error = %q, should mention the missing base image", err)
	}
}

func TestEditValidatesRAMAndCPUs(t *testing.T) {
	for _, c := range []struct{ field, value string }{
		{"ram", "64"}, {"ram", "0"}, {"ram", "lots"}, {"ram", ""},
		{"cpus", "0"}, {"cpus", "-2"}, {"cpus", "many"},
	} {
		e := editFixture(t)
		idx := eRAM
		if c.field == "cpus" {
			idx = eCPUs
		}
		e.inputs[idx].SetValue(c.value)
		if _, err := e.validate(nil); err == nil {
			t.Errorf("%s=%q was accepted", c.field, c.value)
		}
	}
}

// TestEditCarriesRecipeSelection checks the round trip: the form opens with
// the VM's current recipes checked, and saves exactly what is checked in
// recipeNames order.
func TestEditCarriesRecipeSelection(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl",
		RAM: 4096, CPUs: 4, Disk: "8G", SSHPort: 2200, Dir: t.TempDir(),
		Recipes: []string{"xfce.alpine.sh"},
	}
	e := newEdit(v)
	if !e.recipeSel["xfce.alpine.sh"] {
		t.Error("existing recipe did not open pre-checked")
	}

	e.recipeNames = []string{"a.alpine.sh", "b.alpine.sh", "c.alpine.sh"}
	e.recipeSel = map[string]bool{"c.alpine.sh": true, "a.alpine.sh": true}
	a, err := e.validate(nil)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	want := []string{"a.alpine.sh", "c.alpine.sh"} // recipeNames order
	if len(a.vm.Recipes) != 2 || a.vm.Recipes[0] != want[0] || a.vm.Recipes[1] != want[1] {
		t.Errorf("Recipes = %v, want %v", a.vm.Recipes, want)
	}
}

// TestEditDoesNotMutateUntilSaved pins the ordering that keeps a failed save
// from lying: validate builds a copy, so the live VM is untouched until the
// write succeeds.
func TestEditDoesNotMutateUntilSaved(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", RAM: 4096, CPUs: 4,
		Disk: "8G", SSHPort: 2200, Dir: t.TempDir(),
	}
	e := newEdit(v)
	e.inputs[eRAM].SetValue("16384")

	if _, err := e.validate(nil); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if v.RAM != 4096 {
		t.Errorf("validate mutated the live VM: RAM = %d, want 4096", v.RAM)
	}
}

// TestEditTabOrderSkipsDiskInLiveMode mirrors the create form's rule: focus
// must never land on a row viewEdit doesn't draw, or keystrokes silently edit
// an invisible field.
func TestEditTabOrderSkipsDiskInLiveMode(t *testing.T) {
	e := editFixture(t)
	e.mode = "live"
	for _, f := range e.order() {
		if f == eDisk {
			t.Fatalf("eDisk is in the focus order for live mode: %v", e.order())
		}
	}

	e.mode = "disk"
	found := false
	for _, f := range e.order() {
		if f == eDisk {
			found = true
		}
	}
	if !found {
		t.Errorf("eDisk missing from disk-mode focus order: %v", e.order())
	}
}
