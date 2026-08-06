package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// TestSnapshotsModalOpensAndEscCloses mirrors
// TestLogPagerOpensAndEscCloses: "S" opens the modal via its
// snapshotsOpenedMsg round trip (the core.Snapshots read happens in a Cmd off
// the UI goroutine, never synchronously), and esc closes it again, handing
// the detail screen's normal body back.
func TestSnapshotsModalOpensAndEscCloses(t *testing.T) {
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

// TestSnapshotsDeleteRequiresConfirmation proves "d" arms a y/N prompt rather
// than deleting outright, that any key other than "y" cancels it without
// issuing a delete, and that "y" is what actually triggers one. Mirrors the
// "y confirms, anything else cancels" idiom list.go's own delete prompt uses.
func TestSnapshotsDeleteRequiresConfirmation(t *testing.T) {
	sm := newSnapshotsModal("snap-vm-3", []core.Snapshot{
		{Tag: "clean", Size: "1.0 MiB", Created: "2026-01-01 00:00:00"},
	})

	// "d" arms the prompt: no deletion Cmd yet, and the modal now shows the
	// y/N line.
	cmd, closed := sm.update(keyMsg("d"))
	if closed {
		t.Fatalf("arming a delete confirmation must not close the modal")
	}
	if cmd != nil {
		t.Fatalf("\"d\" alone must not delete anything; a Cmd fired before confirmation")
	}
	if sm.pendingDelete == nil || sm.pendingDelete.Tag != "clean" {
		t.Fatalf("expected pendingDelete armed for tag %q, got %+v", "clean", sm.pendingDelete)
	}
	out := ansi.Strip(sm.view(100))
	if !strings.Contains(out, "delete clean? y/N") {
		t.Fatalf("modal view missing the y/N confirmation line:\n%s", out)
	}

	// Anything other than "y" cancels: no Cmd, prompt cleared.
	cmd, closed = sm.update(keyMsg("n"))
	if closed {
		t.Fatalf("cancelling a delete confirmation must not close the modal")
	}
	if cmd != nil {
		t.Fatalf("declining the confirmation must not issue a delete Cmd")
	}
	if sm.pendingDelete != nil {
		t.Fatalf("declining the confirmation must clear pendingDelete")
	}

	// Re-arm, then confirm: "y" is what actually produces the delete Cmd.
	sm.update(keyMsg("d"))
	cmd, closed = sm.update(keyMsg("y"))
	if closed {
		t.Fatalf("confirming a delete must not close the modal itself (it stays open showing the refreshed list)")
	}
	if cmd == nil {
		t.Fatalf("\"y\" must issue the delete Cmd")
	}
	if sm.pendingDelete != nil {
		t.Fatalf("confirming must clear pendingDelete")
	}
}
