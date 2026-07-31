package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

// TestFormTabOrder is a regression test for a user-reported bug: tab focus
// followed the field constant declaration order (name, ram, cpus, disk,
// share, iso, mode) instead of the order viewForm actually renders fields
// in (name, iso, mode, ram, cpus, [disk], share). Worse, in live mode tab
// could land on fDisk even though viewForm doesn't render a disk row (or
// its "❯" marker) in that mode, so keystrokes silently edited an invisible
// field. This asserts the exact visited sequence, forwards and backwards,
// in both modes, including that it wraps at both ends.
func TestFormTabOrder(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		order []int // visual/traversal order, starting from the initial focus (fName)
	}{
		{"live", "live", []int{fName, fISO, fMode, fRAM, fCPUs, fShare, fRecipes}},
		{"disk", "disk", []int{fName, fISO, fMode, fRAM, fCPUs, fDisk, fShare, fRecipes}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := model{form: newForm()}
			m.form.mode = c.mode

			if m.form.focus != fName {
				t.Fatalf("initial focus = %d, want fName (%d)", m.form.focus, fName)
			}

			// Forward: tab len(order) times should visit every position in
			// order and land back on the start (fName) — one full wrap.
			var forward []int
			for i := 0; i < len(c.order); i++ {
				mm, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyTab})
				m = mm.(model)
				forward = append(forward, m.form.focus)
			}
			wantForward := append(append([]int{}, c.order[1:]...), c.order[0])
			if !reflect.DeepEqual(forward, wantForward) {
				t.Fatalf("tab sequence = %v, want %v", forward, wantForward)
			}
			if m.form.focus != fName {
				t.Fatalf("after full forward cycle, focus = %d, want fName (%d)", m.form.focus, fName)
			}

			// Backward: shift+tab len(order) times from fName should retrace
			// the same order in reverse and land back on fName — one full
			// wrap the other way.
			var backward []int
			for i := 0; i < len(c.order); i++ {
				mm, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyShiftTab})
				m = mm.(model)
				backward = append(backward, m.form.focus)
			}
			wantBackward := make([]int, len(c.order))
			for i := range c.order {
				// reverse(order) rotated to start right before fName
				wantBackward[i] = c.order[(len(c.order)-1-i)%len(c.order)]
			}
			if !reflect.DeepEqual(backward, wantBackward) {
				t.Fatalf("shift+tab sequence = %v, want %v", backward, wantBackward)
			}
			if m.form.focus != fName {
				t.Fatalf("after full backward cycle, focus = %d, want fName (%d)", m.form.focus, fName)
			}

			if c.mode == "live" {
				for _, fv := range append(forward, backward...) {
					if fv == fDisk {
						t.Fatalf("fDisk visited in live mode, sequence forward=%v backward=%v", forward, backward)
					}
				}
			} else {
				found := false
				for _, f := range forward {
					if f == fDisk {
						found = true
					}
				}
				if !found {
					t.Fatalf("fDisk not visited in disk mode forward sequence %v", forward)
				}
			}
		})
	}
}

// TestBuildVMFailedDiskCreationLeavesNoTrace drives buildVM down the disk-mode
// path with an invalid qemu-img size and asserts the VM directory (and the
// vm.toml written just before the qemu-img call) does not survive the
// failure. Before the fix, v.Save() ran unconditionally before qemu-img, so
// a bad size left a phantom, unbootable VM behind.
func TestBuildVMFailedDiskCreationLeavesNoTrace(t *testing.T) {
	// Without qemu-img this passes for the wrong reason: buildVM fails
	// because the binary is missing, not because the size is invalid.
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed: skipping disk-creation failure test")
	}
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := config.EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	v := &config.VM{
		Name: "badsize",
		Mode: "disk",
		ISO:  "isos/alpine-standard-3.20.0-x86_64.iso",
		RAM:  1024,
		CPUs: 1,
		Disk: "8Gigs", // invalid qemu-img size
	}

	err := buildVM(v)
	if err == nil {
		t.Fatal("expected buildVM to fail on an invalid disk size")
	}

	dir := filepath.Join(config.Root(), "badsize")
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("VM directory %q survived a failed disk creation: %v", dir, statErr)
	}
}

// TestBuildAssignsSelectedRecipes is the required regression test for C2:
// nothing ever assigned config.VM.Recipes, so "p" was a guaranteed no-op.
// build() must carry exactly the checked recipe names into the VM, in
// recipeNames order, and an empty selection must build fine with an empty
// (not nil-panicking) Recipes slice rather than blocking VM creation.
func TestBuildAssignsSelectedRecipes(t *testing.T) {
	newBuildableForm := func(t *testing.T, name string) formModel {
		t.Helper()
		t.Setenv("STOAT_HOME", t.TempDir())
		f := newForm()
		f.inputs[fName].SetValue(name)
		f.images = []imageOption{{file: "alpine-standard-3.20.0-x86_64.iso", backend: "apkovl", osName: "alpine"}}
		f.imgIdx = 0
		f.recipeNames = []string{"alpha", "beta", "gamma"}
		return f
	}

	t.Run("selected recipes carried through", func(t *testing.T) {
		f := newBuildableForm(t, "withrecipes")
		f.recipeSel = map[string]bool{"gamma": true, "alpha": true}

		vm, err := f.build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		want := []string{"alpha", "gamma"} // recipeNames order, not selection order
		if !reflect.DeepEqual(vm.Recipes, want) {
			t.Fatalf("Recipes = %v, want %v", vm.Recipes, want)
		}
	})

	t.Run("no recipes selected still builds", func(t *testing.T) {
		f := newBuildableForm(t, "norecipes")

		vm, err := f.build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if len(vm.Recipes) != 0 {
			t.Fatalf("Recipes = %v, want empty", vm.Recipes)
		}
	})
}

// TestFormPaneWidthIsStable pins the fix for the box resizing (and
// re-centering) the moment the download block appears: pane() hugs its
// content, and the download stats line is wider than any form row, so the
// whole pane jumped mid-download.
func TestFormPaneWidthIsStable(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenForm, width: 100, height: 40, form: newForm()}
	idle := lipgloss.Width(m.viewForm())

	m.form.fetching = true
	m.form.fetchingOS = "ubuntu"
	m.form.dl = dlStats{done: 600 << 20, total: 1200 << 20, elapsed: 71 * time.Second}
	busy := lipgloss.Width(m.viewForm())

	if idle != busy {
		t.Errorf("pane width changed when the download appeared: %d -> %d", idle, busy)
	}
}

// TestSpaceDownloadsOnImageRow covers the key move: space starts the
// download on the image row (and re-downloads an image that is already
// local), while still toggling recipes on the recipes row.
func TestSpaceDownloadsOnImageRow(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fISO
	got, cmd := m.Update(space)
	if !got.(model).form.fetching {
		t.Error("space on the image row did not start a download")
	}
	if cmd == nil {
		t.Error("space on the image row returned no command")
	}

	// A second press while one is in flight must not start another.
	again, _ := got.(model).Update(space)
	if !again.(model).form.fetching {
		t.Error("in-flight download was cancelled by a second space")
	}

	// The recipes row still toggles.
	r := model{screen: screenForm, form: newForm()}
	r.form.focus = fRecipes
	if len(r.form.recipeNames) > 0 {
		name := r.form.recipeNames[r.form.recipeIdx]
		out, _ := r.Update(space)
		if !out.(model).form.recipeSel[name] {
			t.Errorf("space on the recipes row did not toggle %q", name)
		}
		if out.(model).form.fetching {
			t.Error("space on the recipes row started a download")
		}
	}
}

// TestEnterDoesNotDownload pins the move of downloading off enter: enter on
// an undownloaded image must report what to do rather than silently starting
// a multi-hundred-megabyte fetch.
func TestEnterDoesNotDownload(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fISO
	// Force the selected image to look not-yet-downloaded.
	m.form.images = []imageOption{{entry: &iso.Entry{ID: "x", OS: "ubuntu", Backend: "cloudinit"}, osName: "ubuntu", backend: "cloudinit"}}
	m.form.imgIdx = 0
	m.form.inputs[fName].SetValue("box")

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := out.(model)
	if after.form.fetching {
		t.Error("enter started a download; space is what downloads now")
	}
	if !strings.Contains(after.form.err, "space") {
		t.Errorf("form.err = %q, want it to point at space", after.form.err)
	}
}

// TestQuestionMarkTypesIntoTextFields covers the reported bug where "?" was
// always eaten as the help toggle, so it could never be typed into a text
// field. On a picker row it must still toggle help.
func TestQuestionMarkTypesIntoTextFields(t *testing.T) {
	// newForm()/build() read the data root; without this they see the
	// developer's real ~/.stoat and break the day it holds a VM named "box".
	t.Setenv("STOAT_HOME", t.TempDir())

	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}

	m := model{screen: screenForm, form: newForm()}
	m.form.focus = fName
	m.form.refocus()
	got, _ := m.Update(key)
	after := got.(model)
	if after.showHelp {
		t.Error("? on a text field toggled help instead of typing")
	}
	if v := after.form.inputs[fName].Value(); v != "?" {
		t.Errorf("name field = %q, want %q", v, "?")
	}

	m2 := model{screen: screenForm, form: newForm()}
	m2.form.focus = fISO
	got2, _ := m2.Update(key)
	if !got2.(model).showHelp {
		t.Error("? on the image picker did not toggle help")
	}
}

// TestDownloadSurvivesLeavingTheForm is the regression test for the review's
// C1. Leaving the form with "esc" does not cancel the fetch — there is no
// cancel — so the goroutine keeps writing. If "n" then handed out a fresh
// form, "fetching" would reset to false and a second space would start a
// SECOND download of the same file: two writers interleaving into one .part,
// each verifying its own read stream, so both pass the checksum and the
// image that lands is a corrupt splice marked verified.
func TestDownloadSurvivesLeavingTheForm(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	space := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	m := model{screen: screenForm, form: newForm(), provisioning: map[string]provState{}}
	m.form.focus = fISO

	out, _ := m.Update(space)
	m = out.(model)
	if !m.form.fetching {
		t.Fatal("space did not start a download")
	}

	// esc back to the list; the fetch keeps running.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(model)
	if m.screen != screenList {
		t.Fatalf("esc left screen %v, want the list", m.screen)
	}
	if !m.form.fetching {
		t.Fatal("fetching flag was cleared even though nothing cancelled the download")
	}

	// "n" must NOT hand out a fresh form while that download is live.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = out.(model)
	if !m.form.fetching {
		t.Error(`"n" reset the form and cleared fetching: a second space would corrupt the image`)
	}

	// ...so a second space is refused rather than starting a rival writer.
	before := m.form.fetchingOS
	out, cmd := m.Update(space)
	m = out.(model)
	if cmd != nil {
		t.Error("a second space started another download while one was in flight")
	}
	if m.form.fetchingOS != before {
		t.Errorf("in-flight download was retargeted: %q -> %q", before, m.form.fetchingOS)
	}
}

// TestFetchOutcomesReachTheUserFromTheList covers the other half of C1: a
// download that fails after the user has gone back to the list must still
// report itself. Routed by screen, imageFetchErrMsg was dropped on the floor
// and a checksum failure was simply never mentioned.
func TestFetchOutcomesReachTheUserFromTheList(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	m := model{screen: screenList, form: newForm(), provisioning: map[string]provState{}}
	m.form.fetching = true
	m.form.fetchingOS = "ubuntu"

	out, _ := m.Update(imageFetchErrMsg("ubuntu: checksum mismatch"))
	m = out.(model)
	if m.form.fetching {
		t.Error("a failed fetch left the fetching flag set")
	}
	if !strings.Contains(m.status, "checksum mismatch") {
		t.Errorf("status = %q, want the failure reported", m.status)
	}
}
