package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/novusedge/stoat/internal/config"
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
		f.isos = []string{"alpine-standard-3.20.0-x86_64.iso"}
		f.isoIdx = 0
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
