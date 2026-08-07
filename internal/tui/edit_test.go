package tui

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// editFixture saves a real VM under a fresh STOAT_HOME and opens it for
// editing. It must be a real, saved VM, not a bare struct. The form routes
// through core.Update, which loads by name under config.Root() (see
// core.VM.Name's own comment on why identity is the directory), not from
// whatever the caller holds in memory.
func editFixture(t *testing.T) editModel {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl",
		ISO: "isos/alpine-standard-3.24.1-x86_64.iso",
		RAM: 4096, CPUs: 4, Disk: "8G", Share: "~/vms", SSHPort: 2200,
	}
	if err := v.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	return newEdit(v)
}

func TestParseSizeRejectsRelative(t *testing.T) {
	for _, in := range []string{"+8G", "-4G", "+512M"} {
		if _, err := core.ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) was accepted; it is a RELATIVE size", in)
		}
	}

	// The edit form must refuse it too, not just the parser. A past release
	// wrote this exact string into a live VM's vm.toml (see core.ParseSize's
	// own comment).
	e := editFixture(t)
	e.inputs[eDisk].SetValue("+8G")
	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if _, errText := saveEdit(e.name(), p, false); errText == "" {
		t.Error("a relative disk size was accepted")
	} else if !strings.Contains(errText, "absolute") {
		t.Errorf("error = %q, should say to use an absolute size", errText)
	}
}

// TestEditRefusesDiskShrink covers the ordinary half of the shrink guard:
// growing works, shrinking a normally-stored size is refused.
func TestEditRefusesDiskShrink(t *testing.T) {
	e := editFixture(t) // disk = "8G"
	e.inputs[eDisk].SetValue("4G")

	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if _, errText := saveEdit(e.name(), p, false); errText == "" {
		t.Fatal("shrinking the disk was accepted; that silently destroys data")
	} else if !strings.Contains(errText, "grow") {
		t.Errorf("error = %q, should explain that disks only grow", errText)
	}

	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed: skipping the growth half, which resizes a real image")
	}
	e2 := editFixture(t)
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", e2.vm.DiskPath(), "8G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}
	e2.inputs[eDisk].SetValue("16G")
	p2, err := e2.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	msg, errText := saveEdit(e2.name(), p2, false)
	if errText != "" {
		t.Fatalf("growing the disk was refused: %s", errText)
	}
	saved, ok := msg.(vmSavedMsg)
	if !ok {
		t.Fatalf("saveEdit returned %T, want vmSavedMsg", msg)
	}
	if saved.vm.Disk != "16G" {
		t.Errorf("vm.Disk = %q, want 16G", saved.vm.Disk)
	}
}

// TestEditRefusesDiskShrinkWithUnparseableCurrentSize is a regression test.
// The old form parsed the VM's current disk size, and when that parse
// failed, skipped the shrink check entirely and resized anyway. "+8G" is
// not a hypothetical value: core.ParseSize's own comment says a past
// release wrote it into vm.toml. Such a VM could be "shrunk" to 1G, which
// ran qemu-img resize and truncated the image. core.validateDiskGrow now
// refuses outright when the current size does not parse, and this form
// must not be looser than core.
func TestEditRefusesDiskShrinkWithUnparseableCurrentSize(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "broken-disk", Mode: "disk", OS: "alpine", Backend: "apkovl",
		ISO: "isos/alpine.iso", RAM: 4096, CPUs: 4, Disk: "+8G", SSHPort: 2200,
	}
	if err := v.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	e := newEdit(v)
	e.inputs[eDisk].SetValue("1G")

	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if _, errText := saveEdit(e.name(), p, false); errText == "" {
		t.Fatal("a resize against an unparseable stored disk size (+8G) was accepted; this used to truncate the image")
	}
}

// TestEditRefusesPortCollisionWithForward is a regression test. The old form
// checked a candidate ssh port only against other VMs' SSHPort field, never
// their declared Forwards. core.Update's validateSSHPort, via
// validateForwards, also refuses a port matching any other VM's forward.
// This form must not be looser than that.
func TestEditRefusesPortCollisionWithForward(t *testing.T) {
	e := editFixture(t) // "work", ssh 2200
	other := &config.VM{
		Name: "other", Mode: "disk", OS: "alpine", ISO: "isos/x.iso",
		RAM: 1024, CPUs: 1, SSHPort: 2201,
		Forwards: []config.PortForward{{HostPort: 2300, GuestPort: 8080}},
	}
	if err := other.Save(); err != nil {
		t.Fatalf("save other vm: %v", err)
	}

	e.inputs[eSSHPort].SetValue("2300")
	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if _, errText := saveEdit(e.name(), p, false); errText == "" {
		t.Fatal("a port colliding with another vm's forward was accepted")
	}

	// Keeping its own port is not a collision with itself.
	e.inputs[eSSHPort].SetValue("2200")
	p, err = e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if p.SSHPort != nil {
		t.Error("keeping the vm's own port should not even produce an SSHPort patch")
	}
}

func TestEditBuildPatchRejectsUnparseableNumbers(t *testing.T) {
	for _, c := range []struct{ field, value string }{
		{"ram", "lots"}, {"ram", ""},
		{"cpus", "many"},
		{"ssh", "http"},
	} {
		e := editFixture(t)
		switch c.field {
		case "ram":
			e.inputs[eRAM].SetValue(c.value)
		case "cpus":
			e.inputs[eCPUs].SetValue(c.value)
		case "ssh":
			e.inputs[eSSHPort].SetValue(c.value)
		}
		if _, err := e.buildPatch(); err == nil {
			t.Errorf("%s=%q was accepted by buildPatch", c.field, c.value)
		}
	}
}

// TestEditRAMAndCPURangesEnforcedByCore covers ranges buildPatch no longer
// checks itself: it only refuses what it can't even parse, and defers to
// core.Update (via saveEdit) for the rest, same as the ssh port range.
func TestEditRAMAndCPURangesEnforcedByCore(t *testing.T) {
	for _, c := range []struct{ field, value string }{
		{"ram", "64"}, {"ram", "0"},
		{"cpus", "0"}, {"cpus", "-2"},
	} {
		e := editFixture(t)
		switch c.field {
		case "ram":
			e.inputs[eRAM].SetValue(c.value)
		case "cpus":
			e.inputs[eCPUs].SetValue(c.value)
		}
		p, err := e.buildPatch()
		if err != nil {
			t.Fatalf("buildPatch: %v", err)
		}
		if _, errText := saveEdit(e.name(), p, false); errText == "" {
			t.Errorf("%s=%q was accepted", c.field, c.value)
		}
	}
}

// TestEditCarriesRecipeSelection checks the round trip: the form opens with
// the VM's current recipes checked, and patches exactly what is checked, in
// recipeNames order.
func TestEditCarriesRecipeSelection(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl",
		ISO: "isos/alpine.iso", RAM: 4096, CPUs: 4, Disk: "8G",
		SSHPort: 2200, Recipes: []string{"xfce.alpine.sh"},
	}
	if err := v.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	e := newEdit(v)
	if !e.recipeSel["xfce.alpine.sh"] {
		t.Error("existing recipe did not open pre-checked")
	}

	e.recipeNames = []string{"a.alpine.sh", "b.alpine.sh", "c.alpine.sh"}
	e.recipeSel = map[string]bool{"c.alpine.sh": true, "a.alpine.sh": true}
	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if p.Recipes == nil {
		t.Fatal("Recipes patch not set despite a changed selection")
	}
	want := []string{"a.alpine.sh", "c.alpine.sh"} // recipeNames order
	got := *p.Recipes
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Recipes = %v, want %v", got, want)
	}

	// An unchanged selection must not even produce a Recipes patch: nil
	// means "leave it alone" to core.Update, and setting it to the same
	// value it already has is not the same thing as not mentioning it.
	e2 := newEdit(v)
	e2.recipeNames = []string{"xfce.alpine.sh"}
	p2, err := e2.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if p2.Recipes != nil {
		t.Errorf("Recipes patch set for an untouched selection: %v", *p2.Recipes)
	}
}

// TestEditBuildPatchDoesNotMutateVM pins the ordering that keeps a failed
// save from lying: buildPatch reads the form into a Patch, so the live VM
// is untouched until core.Update's own write succeeds.
func TestEditBuildPatchDoesNotMutateVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 4, Disk: "8G", SSHPort: 2200,
	}
	if err := v.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	e := newEdit(v)
	e.inputs[eRAM].SetValue("16384")

	if _, err := e.buildPatch(); err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if v.RAM != 4096 {
		t.Errorf("buildPatch mutated the live VM: RAM = %d, want 4096", v.RAM)
	}
}

// TestEditTabOrderSkipsDiskInLiveMode mirrors the create form's rule: focus
// must never land on a row viewEdit does not draw. Otherwise keystrokes
// silently edit an invisible field. Mode is immutable, so this test reads
// e.vm.Mode directly, not a separately-tracked form field.
func TestEditTabOrderSkipsDiskInLiveMode(t *testing.T) {
	e := editFixture(t)
	e.vm.Mode = "live"
	for _, f := range e.order() {
		if f == eDisk {
			t.Fatalf("eDisk is in the focus order for live mode: %v", e.order())
		}
	}

	e.vm.Mode = "disk"
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

// TestEditCloudVMSavesWithoutADiskSize is a regression test. A cloud VM
// boots a CoW overlay of its base image and carries no disk size in vm.toml.
// The field opens empty, and a blank field must not turn into a disk patch.
func TestEditCloudVMSavesWithoutADiskSize(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name: "fed", Mode: "cloud", OS: "fedora", Backend: "cloudinit",
		RAM: 4096, CPUs: 4, SSHPort: 2202, Base: "/tmp/base.qcow2",
	}
	if err := v.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	e := newEdit(v)
	if e.inputs[eDisk].Value() != "" {
		t.Fatalf("expected an empty disk field for a cloud VM, got %q", e.inputs[eDisk].Value())
	}

	p, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if p.Disk != nil {
		t.Errorf("a blank disk field asked for a resize to %q", *p.Disk)
	}

	// Filling it in is still read as a grow request...
	e.inputs[eDisk].SetValue("40G")
	p2, err := e.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if p2.Disk == nil || *p2.Disk != "40G" {
		t.Errorf("growing a cloud overlay was not patched through: %+v", p2)
	}
	// ...but core refuses to actually run it until the overlay exists:
	// Create deliberately never creates one (qemu.Start's ensureCloudOverlay
	// does, on first start), so there is nothing yet for qemu-img to grow.
	if _, errText := saveEdit(e.name(), p2, false); errText == "" {
		t.Error("growing an overlay that was never created should be refused")
	}

	// A disk-mode VM still requires one: it has no base to fall back on.
	d := &config.VM{Name: "d", Mode: "disk", ISO: "isos/x.iso", RAM: 1024, CPUs: 1, Disk: "8G", SSHPort: 2203}
	if err := d.Save(); err != nil {
		t.Fatalf("save fixture vm: %v", err)
	}
	de := newEdit(d)
	de.inputs[eDisk].SetValue("")
	dp, err := de.buildPatch()
	if err != nil {
		t.Fatalf("buildPatch: %v", err)
	}
	if dp.Disk == nil {
		t.Fatal("clearing a disk-mode vm's disk field should still reach core as a patch")
	}
	if _, errText := saveEdit(de.name(), dp, false); errText == "" {
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
}

// TestEditModeRowRemoved pins the feature removal (D3): mode is immutable in
// core.Update, so the edit pane must no longer offer a live/disk/cloud row.
func TestEditModeRowRemoved(t *testing.T) {
	e := editFixture(t)
	m := model{screen: screenEdit, width: 100, height: 40, edit: e}
	out := ansi.Strip(m.viewEdit())
	if strings.Contains(out, "live") || strings.Contains(out, "cloud") {
		t.Errorf("edit pane still shows a mode row: %s", out)
	}
}
