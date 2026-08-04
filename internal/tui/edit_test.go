package tui

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

func recipesInstall() error { return recipes.Install() }

func editFixture(t *testing.T) editModel {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	return newEdit(&config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl",
		ISO: "isos/alpine-standard-3.24.1-x86_64.iso",
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

// TestEditRefusesDiskShrink covers the friendly half of the shrink guard.
// qemu-img itself refuses a shrink without --shrink (verified: exit 1,
// "Use the --shrink option"), and saveEdit never passes it. So this is not
// the last line of defence, it is the one that answers in stoat's own words
// instead of a raw qemu-img error, and before anything is written.
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
		ISO: "isos/alpine.iso", RAM: 4096, CPUs: 4, Disk: "8G",
		SSHPort: 2200, Dir: t.TempDir(),
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
		Name: "work", Mode: "disk", OS: "alpine", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 4, Disk: "8G", SSHPort: 2200, Dir: t.TempDir(),
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

// TestEditCloudVMSavesWithoutADiskSize is the regression test for a reported
// bug: a cloud VM boots a CoW overlay of its base image and carries no disk
// size in vm.toml at all, so the field opens empty, and validate demanded
// one, making every cloud VM impossible to save with "disk size is required".
func TestEditCloudVMSavesWithoutADiskSize(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "fed", Mode: "cloud", OS: "fedora", Backend: "cloudinit",
		RAM: 4096, CPUs: 4, SSHPort: 2202, Dir: t.TempDir(), Base: "/tmp/base.qcow2",
	}
	e := newEdit(v)
	if e.inputs[eDisk].Value() != "" {
		t.Fatalf("expected an empty disk field for a cloud VM, got %q", e.inputs[eDisk].Value())
	}

	a, err := e.validate(nil)
	if err != nil {
		t.Fatalf("a cloud VM could not be saved: %v", err)
	}
	if a.resizeTo != "" {
		t.Errorf("a blank disk field asked for a resize to %q", a.resizeTo)
	}

	// Filling it in is still a grow request.
	e.inputs[eDisk].SetValue("40G")
	a, err = e.validate(nil)
	if err != nil {
		t.Fatalf("growing a cloud overlay was refused: %v", err)
	}
	if a.resizeTo != "40G" {
		t.Errorf("resizeTo = %q, want 40G", a.resizeTo)
	}

	// A disk-mode VM still requires one, since it has no base to fall back on.
	d := newEdit(&config.VM{Name: "d", Mode: "disk", ISO: "isos/x.iso", RAM: 1024, CPUs: 1, SSHPort: 2200, Dir: t.TempDir()})
	d.inputs[eDisk].SetValue("")
	if _, err := d.validate(nil); err == nil {
		t.Error("disk mode accepted an empty disk size")
	}
}

// TestEditChangeMarkers covers the diff display: an untouched form reports no
// changes, and a touched field says what it was.
func TestEditChangeMarkers(t *testing.T) {
	e := editFixture(t)
	if e.dirty() {
		t.Error("an untouched form reports itself dirty")
	}
	for i := 0; i < eFieldCount; i++ {
		if was := e.changed(i); was != "" {
			t.Errorf("field %d reports a change (%q) before being edited", i, was)
		}
	}

	e.inputs[eRAM].SetValue("8192")
	if was := e.changed(eRAM); was != "4096" {
		t.Errorf("changed(eRAM) = %q, want the old value 4096", was)
	}
	if !e.dirty() {
		t.Error("an edited form does not report itself dirty")
	}

	// Toggling a recipe counts as a change even with every text field intact.
	clean := editFixture(t)
	clean.recipeNames = []string{"x.alpine.sh"}
	clean.recipeSel = map[string]bool{"x.alpine.sh": true}
	if !clean.dirty() {
		t.Error("selecting a recipe did not mark the form dirty")
	}

	// Mode changes count too.
	md := editFixture(t)
	md.mode = "live"
	if !md.dirty() {
		t.Error("changing the mode did not mark the form dirty")
	}
}

// TestParseSizeRejectsRelative covers a size qemu-img accepts but means
// something else by: "+8G" GROWS BY 8G, while strconv.ParseFloat reads "+8"
// as 8, so the grow check would clear it as "grow to 8G". A 4G disk would
// become 12G while vm.toml recorded the literal "+8G", disagreeing with the
// disk from then on. Verified against qemu-img: an 8G image given "+8G"
// becomes 16G.
func TestParseSizeRejectsRelative(t *testing.T) {
	for _, in := range []string{"+8G", "-4G", "+512M"} {
		if _, err := parseSize(in); err == nil {
			t.Errorf("parseSize(%q) was accepted; it is a RELATIVE size", in)
		}
	}

	// And it must be refused at the form level, not just in the parser.
	e := editFixture(t)
	e.inputs[eDisk].SetValue("+8G")
	if _, err := e.validate(nil); err == nil {
		t.Error("a relative disk size was accepted by validate")
	} else if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %q, should say to use an absolute size", err)
	}
}

// TestModeSwitchToDiskNeedsADiskFile is the regression test for the worst
// finding in review: the create form records a disk size for every non-cloud
// VM, but only creates disk.qcow2 in disk mode. So a live VM has "8G" in its
// vm.toml and no file, and flipping it to disk mode left the size unchanged,
// which meant no resize ran, the save succeeded, and the next start died on a
// missing image with nothing pointing back at this form.
func TestModeSwitchToDiskNeedsADiskFile(t *testing.T) {
	// saveEdit shells out to qemu-img to create the missing disk, so this
	// test needs the real tool; skip rather than fail where it isn't
	// installed (CI), which would make the suite permanently red.
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed: skipping disk-creation test")
	}
	t.Setenv("STOAT_HOME", t.TempDir())
	dir := t.TempDir()
	v := &config.VM{
		Name: "live1", Mode: "live", OS: "alpine", Backend: "apkovl",
		ISO: "isos/alpine.iso", RAM: 4096, CPUs: 4, Disk: "8G",
		SSHPort: 2200, Dir: dir,
	}
	if _, err := os.Stat(v.DiskPath()); !os.IsNotExist(err) {
		t.Fatalf("fixture should have no disk file: %v", err)
	}

	e := newEdit(v)
	e.mode = "disk"
	a, err := e.validate(nil)
	if err != nil {
		t.Fatalf("live -> disk was refused: %v", err)
	}

	// saveEdit must create the missing disk rather than writing a vm.toml
	// that promises one.
	msg := saveEdit(a, false)()
	if s, ok := msg.(statusMsg); ok {
		t.Fatalf("save failed: %s", s)
	}
	if _, err := os.Stat(v.DiskPath()); err != nil {
		t.Errorf("switching to disk mode did not create the disk: %v", err)
	}
}

// TestModeSwitchOffCloudNeedsAnISO covers the mirror of the cloud-needs-a-base
// guard. A cloud VM has no ISO, and ISOPath() on an empty ISO resolves to the
// data root DIRECTORY, so qemu would be handed "-cdrom ~/.stoat" and refuse
// to start.
func TestModeSwitchOffCloudNeedsAnISO(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	e := newEdit(&config.VM{
		Name: "fed", Mode: "cloud", OS: "fedora", Backend: "cloudinit",
		RAM: 4096, CPUs: 4, SSHPort: 2202, Dir: t.TempDir(), Base: "/tmp/b.qcow2",
	})
	for _, mode := range []string{"live", "disk"} {
		e.mode = mode
		if _, err := e.validate(nil); err == nil {
			t.Errorf("cloud -> %s was accepted with no iso to boot", mode)
		} else if !strings.Contains(err.Error(), "iso") {
			t.Errorf("error = %q, should mention the missing iso", err)
		}
	}
}

// TestModeSwitchResyncsRecipes covers the cross-backend leak: the recipe list
// is per-backend and the backend follows the mode, so a cloud fragment must
// stop being on offer (and stop being saved) once the VM is no longer cloud.
func TestModeSwitchResyncsRecipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipesInstall(); err != nil {
		t.Fatal(err)
	}
	e := newEdit(&config.VM{
		Name: "u", Mode: "cloud", OS: "ubuntu", Backend: "cloudinit",
		ISO: "isos/u.iso", RAM: 4096, CPUs: 4, SSHPort: 2203,
		Dir: t.TempDir(), Base: "/tmp/b.qcow2",
		Recipes: []string{"xfce.cloud.yaml"},
	})
	if !e.recipeSel["xfce.cloud.yaml"] {
		t.Fatal("cloud fragment did not open pre-checked")
	}

	e.mode = "live"
	e.syncRecipes()
	if e.recipeSel["xfce.cloud.yaml"] {
		t.Error("a cloud fragment survived the switch to live mode")
	}
	for _, n := range e.recipeNames {
		if strings.HasSuffix(n, ".cloud.yaml") {
			t.Errorf("live mode still offers the cloud fragment %q", n)
		}
	}
}
