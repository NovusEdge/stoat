package tui

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// TestSnapshotsModalOpensAndEscCloses mirrors TestLogPagerOpensAndEscCloses.
// "S" opens the modal through its snapshotsOpenedMsg round trip: the
// core.Snapshots read runs in a Cmd off the UI goroutine, never
// synchronously. esc closes the modal and hands back the detail screen's
// normal body.
func TestSnapshotsModalOpensAndEscCloses(t *testing.T) {
	// openSnapshots' Cmd calls core.Snapshots, which shells to qemu-img.
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}
	// core.Snapshots (which openSnapshots calls) resolves the VM by name
	// under config.Root(), not by v.Dir, so the fixture has to live there;
	// see TestLogPagerOpensAndEscCloses for the same requirement.
	t.Setenv("STOAT_HOME", t.TempDir())
	cv := &config.VM{Name: "snap-vm", Mode: "disk", Disk: "1G"}
	if err := cv.Save(); err != nil {
		t.Fatalf("saving fixture vm.toml: %v", err)
	}
	v, err := core.Get(cv.Name)
	if err != nil {
		t.Fatalf("core.Get: %v", err)
	}

	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)

	newM, cmd := m.updateDetail(keyMsg("S"))
	got := newM.(model)
	if got.detail.snapshots != nil {
		t.Fatalf("modal should not open synchronously; opening reads snapshots off the UI goroutine")
	}
	if cmd == nil {
		t.Fatalf("expected a Cmd to open the snapshots modal")
	}
	msg := cmd()
	opened, ok := msg.(snapshotsOpenedMsg)
	if !ok {
		t.Fatalf("expected snapshotsOpenedMsg, got %T (%v)", msg, msg)
	}
	if opened.err != nil {
		t.Fatalf("core.Snapshots refused a fresh disk VM with no snapshots yet: %v", opened.err)
	}

	newM, _ = got.updateDetail(opened)
	got = newM.(model)
	if got.detail.snapshots == nil {
		t.Fatalf("snapshotsOpenedMsg should have opened the modal")
	}

	out := ansi.Strip(got.viewDetail())
	if !strings.Contains(out, "snapshots") {
		t.Fatalf("modal view missing its own title:\n%s", out)
	}
	if !strings.Contains(out, "No snapshots") {
		t.Fatalf("a VM with no snapshots must show an empty-state line, not a blank box:\n%s", out)
	}

	// A VM with nothing to select must not let "d"/"r" arm a confirmation
	// for a snapshot that doesn't exist: selected() returns false on an
	// empty list, and both handlers must no-op rather than reach into a
	// zero-value core.Snapshot.
	sm := got.detail.snapshots
	if cmd, closed := sm.update(keyMsg("d")); cmd != nil || closed {
		t.Fatalf("\"d\" on an empty snapshot list must be a no-op, got cmd=%v closed=%v", cmd, closed)
	}
	if sm.pendingDelete != nil {
		t.Fatalf("\"d\" on an empty snapshot list must not arm pendingDelete, got %+v", sm.pendingDelete)
	}
	if cmd, closed := sm.update(keyMsg("r")); cmd != nil || closed {
		t.Fatalf("\"r\" on an empty snapshot list must be a no-op, got cmd=%v closed=%v", cmd, closed)
	}
	if sm.pendingRestore != nil {
		t.Fatalf("\"r\" on an empty snapshot list must not arm pendingRestore, got %+v", sm.pendingRestore)
	}

	newM, _ = got.updateDetail(keyMsg("esc"))
	got = newM.(model)
	if got.detail.snapshots != nil {
		t.Fatalf("esc should have closed the modal")
	}
	// Back to the ordinary detail body, not left mid-modal.
	if got.screen != screenDetail {
		t.Fatalf("esc closing the modal must not also navigate back to the list; screen=%v", got.screen)
	}
}

// TestSnapshotsModalEscTakesPriorityOverDetailBindings mirrors
// TestLogPagerEscTakesPriorityOverDetailBindings: while the modal is open,
// esc closes IT rather than falling through to the detail screen's own
// esc/back binding.
func TestSnapshotsModalEscTakesPriorityOverDetailBindings(t *testing.T) {
	v := core.VM{Name: "snap-vm-2", Mode: "disk", Paths: core.Paths{Dir: t.TempDir()}}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(v)
	m.detail.snapshots = newSnapshotsModal(v.Name, nil)

	newM, _ := m.updateDetail(keyMsg("esc"))
	got := newM.(model)
	if got.screen != screenDetail {
		t.Fatalf("esc with the modal open must not also navigate back to the list; screen=%v", got.screen)
	}
	if got.detail.snapshots != nil {
		t.Fatalf("esc must close the modal")
	}
}

// TestSnapshotsDestructiveActionsRequireConfirmation proves both "d"
// (delete) and "r" (restore) arm a y/N prompt instead of acting outright.
// Any key other than "y" cancels the prompt without issuing the action, and
// "y" is what triggers it. Restore gets the same gate as delete: it
// discards everything since the snapshot was taken, with no way back, even
// though it leaves the snapshot itself in place. This mirrors the "y
// confirms, anything else cancels" idiom list.go's own delete prompt uses.
func TestSnapshotsDestructiveActionsRequireConfirmation(t *testing.T) {
	cases := []struct {
		key      string // the key that arms the prompt
		wantLine string // the confirmation line the view must show
		pending  func(*snapshotsModal) *core.Snapshot
	}{
		{"d", "delete clean? y/N", func(sm *snapshotsModal) *core.Snapshot { return sm.pendingDelete }},
		{"r", "restore clean? y/N", func(sm *snapshotsModal) *core.Snapshot { return sm.pendingRestore }},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			sm := newSnapshotsModal("snap-vm-3", []core.Snapshot{
				{Tag: "clean", Size: "1.0 MiB", Created: "2026-01-01 00:00:00"},
			})

			// The key arms the prompt: no Cmd yet, and the modal now shows
			// the y/N line.
			cmd, closed := sm.update(keyMsg(tc.key))
			if closed {
				t.Fatalf("arming a %q confirmation must not close the modal", tc.key)
			}
			if cmd != nil {
				t.Fatalf("%q alone must not act; a Cmd fired before confirmation", tc.key)
			}
			pending := tc.pending(sm)
			if pending == nil || pending.Tag != "clean" {
				t.Fatalf("expected a pending confirmation armed for tag %q, got %+v", "clean", pending)
			}
			out := ansi.Strip(sm.view(100))
			if !strings.Contains(out, tc.wantLine) {
				t.Fatalf("modal view missing the y/N confirmation line %q:\n%s", tc.wantLine, out)
			}

			// Anything other than "y" cancels: no Cmd, prompt cleared.
			cmd, closed = sm.update(keyMsg("x"))
			if closed {
				t.Fatalf("cancelling a %q confirmation must not close the modal", tc.key)
			}
			if cmd != nil {
				t.Fatalf("declining the confirmation must not issue a Cmd")
			}
			if tc.pending(sm) != nil {
				t.Fatalf("declining the confirmation must clear the pending state")
			}

			// Re-arm, then confirm: "y" is what actually produces the Cmd.
			sm.update(keyMsg(tc.key))
			cmd, closed = sm.update(keyMsg("y"))
			if closed {
				t.Fatalf("confirming must not close the modal itself (it stays open showing the refreshed list)")
			}
			if cmd == nil {
				t.Fatalf("\"y\" must issue the Cmd")
			}
			if tc.pending(sm) != nil {
				t.Fatalf("confirming must clear the pending state")
			}
		})
	}
}
