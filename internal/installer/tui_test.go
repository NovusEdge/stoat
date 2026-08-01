package installer

import (
	"os"
	"path/filepath"
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

// checkTable's own comment documents a real lipgloss v2.0.5 bug: a headerless
// table that ever calls .Height() silently drops its last row. This is the
// regression test that comment promises but does not enforce on its own --
// internal/tui/fields_test.go's TestFieldsKeepsEveryRow is the sibling for
// the main TUI's equivalent landmine. It fails the moment someone adds a
// width/height clamp to checkTable to fit a narrow terminal without
// re-reading the warning.
func TestCheckTableKeepsEveryRow(t *testing.T) {
	m := Model{checks: []Check{
		{Name: "qemu-system-x86_64", OK: true, Detail: "/usr/bin"},
		{Name: "qemu-img", OK: true, Detail: "/usr/bin"},
		{Name: "ssh", OK: true, Detail: "/usr/bin"},
		{Name: "xorriso", OK: false, Detail: "not found"},
		{Name: "/dev/kvm", OK: true, Detail: "read/write"},
	}}
	out := m.checkTable()

	for _, c := range m.checks {
		if !strings.Contains(out, c.Name) {
			t.Errorf("checkTable is missing %q:\n%s", c.Name, out)
		}
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(m.checks) {
		t.Fatalf("got %d rows, want %d:\n%s", len(lines), len(m.checks), out)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "/dev/kvm") {
		t.Errorf("last row is not /dev/kvm -- the row v2.0.5 drops once .Height() is set: %q", last)
	}
}

// Typing a relative path and pressing enter must resolve it to an absolute
// path before it becomes m.dir -- a bare "bin" left relative would write a
// PATH entry any cwd can shadow. This is the test that would have caught it:
// it drives the real key.Matches(keys.Install) dispatch in m.key, not a
// phase set by hand.
func TestDirPromptTypedPathBecomesAbsolute(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	next, _ := m.Update(checksDoneMsg{checks: nil})
	m = next.(Model)

	// New() pre-fills the input with the default dir; clear it before typing
	// so the keystroke below is the whole value, not an append to the default.
	m.input.SetValue("")
	next, _ = m.Update(tea.KeyPressMsg{Text: "custom/dir"})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if m.phase != phaseBuild {
		t.Fatalf("phase = %v, want phaseBuild", m.phase)
	}
	if cmd == nil {
		t.Fatal("enter at the dir prompt must dispatch buildCmd")
	}
	want, err := filepath.Abs("custom/dir")
	if err != nil {
		t.Fatal(err)
	}
	if m.dir != want {
		t.Errorf("dir = %q, want the resolved absolute path %q", m.dir, want)
	}
}

// Clearing the prompt and pressing enter must fall back to the default dir,
// never install to "". This is the TrimSpace/non-empty guard in m.key.
func TestDirPromptEmptyInputKeepsDefault(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	next, _ := m.Update(checksDoneMsg{checks: nil})
	m = next.(Model)
	want := m.dir // the default DefaultDir value New() already put in m.dir

	m.input.SetValue("   ") // whitespace-only counts as "nothing typed"
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)

	if m.phase != phaseBuild {
		t.Fatalf("phase = %v, want phaseBuild", m.phase)
	}
	if m.dir != want {
		t.Errorf("dir = %q, want the untouched default %q", m.dir, want)
	}
	if m.dir == "" {
		t.Fatal("dir must never be empty")
	}
}

// "~" and "~/sub" must expand against the model's home, not the process's.
func TestDirPromptExpandsHome(t *testing.T) {
	tests := []struct{ typed, want string }{
		{"~", "/home/x"},
		{"~/sub", "/home/x/sub"},
	}
	for _, tt := range tests {
		t.Run(tt.typed, func(t *testing.T) {
			m := newTestModel(t, "/usr/bin")
			next, _ := m.Update(checksDoneMsg{checks: nil})
			m = next.(Model)

			m.input.SetValue("")
			next, _ = m.Update(tea.KeyPressMsg{Text: tt.typed})
			m = next.(Model)
			next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			m = next.(Model)

			if m.dir != tt.want {
				t.Errorf("dir = %q, want %q", m.dir, tt.want)
			}
		})
	}
}

// Ctrl+C at the dir prompt -- before anything is built or installed -- must
// not let `just setup` report success. binPath is still empty at this point,
// which is exactly what Failed() and done() key on to tell this apart from
// quitting after a successful install.
func TestCtrlCAtDirPromptFailsTheRun(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	next, _ := m.Update(checksDoneMsg{checks: nil})
	m = next.(Model)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(Model)

	if !m.Failed() {
		t.Error("Failed() = false after ctrl+c before anything was installed")
	}
	if m.phase != phaseDone {
		t.Errorf("phase = %v, want phaseDone", m.phase)
	}
	if cmd == nil {
		t.Fatal("ctrl+c must still quit the program")
	}
	view := m.View().Content
	if !strings.Contains(view, "cancelled") || !strings.Contains(view, "nothing was installed") {
		t.Errorf("view does not say the run was cancelled:\n%s", view)
	}
}

// Ctrl+C at the rc prompt is different: the binary is already on disk by
// then, so walking away from the optional PATH question must stay as
// non-fatal as answering "n" to it -- see the settled design in Failed's
// comment.
func TestCtrlCAfterInstallStaysNonFatal(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	m.dir = "/home/x/.local/bin"
	m.phase = phaseRC
	m.binPath = "/home/x/.local/bin/stoat"
	m.rcPath, m.rcLine = ShellRC("/usr/bin/zsh", "/home/x", m.dir)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(Model)

	if m.Failed() {
		t.Error("Failed() = true after ctrl+c once the binary was already installed")
	}
	view := m.View().Content
	if !strings.Contains(view, m.binPath) {
		t.Errorf("done view lost the install confirmation after ctrl+c:\n%s", view)
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

// Declining must write nothing, still finish cleanly, and -- since the user
// said they will add it by hand -- still show them the line to add.
func TestDecliningRCGoesToDone(t *testing.T) {
	m := newTestModel(t, "/usr/bin")
	m.dir = "/home/x/.local/bin"
	m.phase = phaseRC
	m.binPath = "/home/x/.local/bin/stoat"
	m.rcPath, m.rcLine = ShellRC("/usr/bin/zsh", "/home/x", m.dir)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = next.(Model)

	if m.phase != phaseDone {
		t.Errorf("phase = %v, want phaseDone", m.phase)
	}
	if m.rcAdded {
		t.Error("declining must not report a write")
	}
	view := m.View().Content
	if !strings.Contains(view, m.rcLine) {
		t.Errorf("done view drops the line after declining it:\n%s", view)
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

// A failed rc write is recoverable — the build and install already succeeded
// by the time AppendRC runs — so it must not be reported as a hard failure:
// Failed() must stay false, and the done screen must still show the install
// alongside the line the user needs to add themselves.
func TestFailedRCWriteDoesNotFailTheInstall(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTestModel(t, "/usr/bin")
	m.dir = "/home/x/.local/bin"
	m.phase = phaseRC
	m.binPath = "/home/x/.local/bin/stoat"
	// blocker is a regular file, so anything under it as a parent dir fails
	// both the read and the MkdirAll inside AppendRC — without touching
	// anything outside t.TempDir().
	m.rcPath = filepath.Join(blocker, ".zshrc")
	m.rcLine = `export PATH="/home/x/.local/bin:$PATH"`

	next, _ := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m = next.(Model)

	if m.Failed() {
		t.Error("Failed() = true after a recoverable rc-write failure")
	}
	if m.phase != phaseDone {
		t.Errorf("phase = %v, want phaseDone", m.phase)
	}
	view := m.View().Content
	if !strings.Contains(view, m.binPath) {
		t.Errorf("done view lost the install confirmation after a failed rc write:\n%s", view)
	}
	if !strings.Contains(view, m.rcLine) {
		t.Errorf("done view does not tell the user the line to add themselves:\n%s", view)
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

// A hard failure must not swallow the host advice: a user whose install died
// on, say, a permission error still needs to know what to fix before their
// first VM, and this is the only place that guidance is ever given.
func TestFailedInstallStillShowsHostAdvice(t *testing.T) {
	m := newTestModel(t, "/home/x/.local/bin")
	m.checks = []Check{
		{Name: "xorriso", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed libisoburn"}},
	}

	next, _ := m.Update(errMsg{err: &BuildError{Output: "internal/qemu/run.go:1:1: syntax error"}})
	m = next.(Model)

	view := m.View().Content
	if !strings.Contains(view, "before your first VM:") {
		t.Errorf("failed install view drops the host advice:\n%s", view)
	}
	if !strings.Contains(view, "sudo pacman -S --needed libisoburn") {
		t.Errorf("failed install view drops the fix command:\n%s", view)
	}
}

// tempBuildDirs finds this package's own leftovers, not just anything in
// os.TempDir() -- the real temp dir is shared with everything else running
// on the machine, so a bare entry count would be flaky.
func tempBuildDirs() []string {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "stoat-build-*"))
	return matches
}

// A repo dir with no ./cmd/stoat fails the build immediately, which is the
// cheap way to drive buildCmd's error path without an actual go build.
func TestBuildCmdRemovesTempDirOnBuildFailure(t *testing.T) {
	before := len(tempBuildDirs())

	msg := buildCmd(t.TempDir(), "v0.0.0")()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg building a dir with no cmd/stoat, got %T: %+v", msg, msg)
	}
	if after := len(tempBuildDirs()); after != before {
		t.Errorf("buildCmd left a stoat-build-* dir behind: before %d, after %d", before, after)
	}
}

// installCmd owns the temp dir buildCmd created from the moment it is
// handed the built binary: it must remove it once the copy is done, success
// or failure alike.
func TestInstallCmdRemovesBuildTempDirOnSuccess(t *testing.T) {
	tmp, err := os.MkdirTemp("", "stoat-build-*")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "stoat")
	if err := os.WriteFile(src, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := installCmd(src, t.TempDir())()
	if _, ok := msg.(installedMsg); !ok {
		t.Fatalf("expected installedMsg, got %T: %+v", msg, msg)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("installCmd left the build temp dir behind on success: stat err = %v", err)
	}
}

func TestInstallCmdRemovesBuildTempDirOnFailure(t *testing.T) {
	tmp, err := os.MkdirTemp("", "stoat-build-*")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "stoat") // never created, so Install's Open fails

	msg := installCmd(src, t.TempDir())()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg for a missing src binary, got %T: %+v", msg, msg)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("installCmd left the build temp dir behind on failure: stat err = %v", err)
	}
}
