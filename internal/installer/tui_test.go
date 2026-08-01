package installer

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func newTestModel(t *testing.T, pathEnv string) Model {
	t.Helper()
	return New(t.TempDir(), "/home/x", "/usr/bin/zsh", pathEnv, "")
}

// The transcript must reach the dir prompt once checks land, and must carry a
// row per check.
func TestChecksAdvanceToDirPrompt(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")

	next, _ := m.Update(checksDoneMsg{checks: []Check{
		{Name: "qemu-img", OK: true, Detail: "/usr/bin"},
		{Name: "xorriso", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed libisoburn"}},
	}})
	m = next.(Model)

	if m.phase != phaseDir {
		t.Errorf("phase = %v, want phaseDir", m.phase)
	}
	view := m.View().Content
	for _, want := range []string{"qemu-img", "/usr/bin", "xorriso", "not found"} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

// When the target dir is already on PATH there is nothing to ask, so the rc
// prompt must be skipped entirely.
func TestInstalledSkipsRCPromptWhenOnPath(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")
	m.dir = "/home/x/.local/bin"

	next, _ := m.Update(installedMsg{path: "/home/x/.local/bin/stoat"})
	m = next.(Model)

	if m.phase != phaseDone {
		t.Errorf("phase = %v, want phaseDone — the dir is already on PATH", m.phase)
	}
}

// When it is not on PATH, the user must be asked before anything is written.
func TestInstalledAsksAboutRCWhenNotOnPath(t *testing.T) {
	m := newTestModel(t, "/usr/bin:/bin")
	m.dir = "/home/x/.local/bin"

	next, _ := m.Update(installedMsg{path: "/home/x/.local/bin/stoat"})
	m = next.(Model)

	if m.phase != phaseRC {
		t.Fatalf("phase = %v, want phaseRC", m.phase)
	}
	view := m.View().Content
	if !strings.Contains(view, ".zshrc") {
		t.Errorf("view does not name the rc file:\n%s", view)
	}
	if !strings.Contains(view, "[Y/n]") {
		t.Errorf("view does not prompt:\n%s", view)
	}
}

// Declining must write nothing and still finish cleanly.
func TestDecliningRCGoesToDone(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	m.dir = "/home/x/.local/bin"
	m.phase = phaseRC

	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)

	if m.phase != phaseDone {
		t.Errorf("phase = %v, want phaseDone", m.phase)
	}
	if m.rcAdded {
		t.Error("declining must not report a write")
	}
}

// Every unfixed problem must appear on the done screen with its fix, or the
// check screen was pointless.
func TestDoneListsEveryProblemWithItsFix(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")
	m.phase = phaseDone
	m.version = "v0.3.1"
	m.binPath = "/home/x/.local/bin/stoat"
	m.checks = []Check{
		{Name: "qemu-img", OK: true, Detail: "/usr/bin"},
		{Name: "xorriso", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed libisoburn"}},
		{Name: "/dev/kvm", OK: false, Detail: "permission denied", Fix: []string{`sudo usermod -aG kvm "$USER"`}},
	}

	view := m.View().Content
	for _, want := range []string{
		"v0.3.1",
		"/home/x/.local/bin/stoat",
		"xorriso",
		"sudo pacman -S --needed libisoburn",
		"/dev/kvm",
		`sudo usermod -aG kvm "$USER"`,
	} {
		if !strings.Contains(view, want) {
			t.Errorf("done view is missing %q:\n%s", want, view)
		}
	}

	// The transcript keeps the check block, so every check name appears above.
	// What must contain problems only is the trailing advice section.
	_, advice, found := strings.Cut(view, "before your first VM:")
	if !found {
		t.Fatalf("no advice section in the done view:\n%s", view)
	}
	if strings.Contains(advice, "qemu-img") {
		t.Errorf("the advice section lists a passing check:\n%s", advice)
	}
}

// qemu-img and qemu-system-x86_64 share the qemu-full package on Arch, so two
// checks can carry the identical Fix line. The done screen must print it once,
// not once per check — that dedup belongs to the display layer, not Check.
func TestDoneDedupesIdenticalFixCommands(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")
	m.phase = phaseDone
	m.binPath = "/home/x/.local/bin/stoat"
	m.checks = []Check{
		{Name: "qemu-system-x86_64", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed qemu-full"}},
		{Name: "qemu-img", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed qemu-full"}},
	}

	view := m.View().Content
	want := "sudo pacman -S --needed qemu-full"
	if got := strings.Count(view, want); got != 1 {
		t.Errorf("fix command appears %d times, want 1:\n%s", got, view)
	}
}

// A build failure must show go's actual output, not just an exit status.
func TestBuildFailureShowsCompilerOutput(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")

	next, _ := m.Update(errMsg{err: &BuildError{Output: "internal/qemu/run.go:1:1: syntax error"}})
	m = next.(Model)

	if !m.Failed() {
		t.Error("Failed() = false after a build error")
	}
	if !strings.Contains(m.View().Content, "syntax error") {
		t.Errorf("view does not show the compiler output:\n%s", m.View().Content)
	}
}
