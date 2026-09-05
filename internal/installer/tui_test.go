package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// lipgloss v2.0.5 drops the last row of a headerless table once it calls
// .Height(); see checkTable's comment. This test pins that behavior.
// internal/tui/fields_test.go's TestFieldsKeepsEveryRow covers the same bug
// in the main TUI. A width or height clamp added to checkTable without
// checking this test would break it silently.
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
		t.Errorf("last row is not /dev/kvm (the row v2.0.5 drops once .Height() is set): %q", last)
	}
}

// A typed relative path must resolve to absolute before it becomes m.dir.
// A bare "bin" left relative lets any cwd shadow the PATH entry. This test
// drives the real key.Matches(keys.Install) dispatch in m.key, not a
// hand-set phase.
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

// Ctrl+C at the dir prompt must not let `just setup` report success, before
// anything is built or installed. binPath is still empty at this point.
// Failed() and done() use that to tell this apart from quitting after a
// successful install.
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

// Ctrl+C at the rc prompt is different: the binary is already on disk.
// Walking away from the optional PATH question must stay as non-fatal as
// answering "n". See Failed's comment for the design.
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
		t.Errorf("phase = %v, want phaseDone: the dir is already on PATH", m.phase)
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

// Declining must write nothing, still finish cleanly, and, since the user
// said they will add it by hand, still show them the line to add.
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
		{Name: "git", Optional: true, Detail: "not found", Fix: []string{"sudo pacman -S --needed git"}},
		{Name: "xorriso", OK: false, Detail: "not found", Fix: []string{"sudo pacman -S --needed libisoburn"}},
		{Name: "/dev/kvm", OK: false, Detail: "permission denied", Fix: []string{`sudo usermod -aG kvm "$USER"`}},
	}

	view := m.View().Content
	for _, want := range []string{
		"v0.3.1",
		"/home/x/.local/bin/stoat",
		"git",
		"sudo pacman -S --needed git",
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
	if !strings.Contains(advice, "git") || !strings.Contains(advice, "sudo pacman -S --needed git") {
		t.Errorf("the advice section omits optional Git repair guidance:\n%s", advice)
	}
}

// qemu-img and qemu-system-x86_64 share the qemu-full package on Arch, so two
// checks can carry the identical Fix line. The done screen must print it once,
// not once per check: that dedup belongs to the display layer, not Check.
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

// A failed rc write is recoverable: the build and install already succeeded
// by the time AppendRC runs. Failed() must stay false. The done screen must
// still show the install and the line the user needs to add themselves.
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
	// both the read and the MkdirAll inside AppendRC, without touching
	// anything outside t.TempDir().
	m.rcPath = filepath.Join(blocker, ".zshrc")
	_, m.rcLine = ShellRC(m.shell, m.home, m.dir)

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

// ansiRE strips SGR colour codes so pastedRCLine reads a rendered line's
// real text. The theme's colours do not matter. Only the characters that
// would land in the terminal matter, since those are what a paste carries.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// longRCDir is long enough that its rc line overflows both 60 and 80
// columns at every one of tui.go's three render indents, so every test
// below actually exercises wrapping rather than the single-line fast path.
var longRCDir = "/home/exampleuser/" + strings.Repeat("wide-prefix-segment/", 6) + "bin"

// rcLineBlock finds the rc-line block in a rendered View. It returns every
// ANSI-stripped physical line indented by exactly indent, starting right
// after the line containing anchor. Each returned line keeps its indent.
// Every line but the last keeps its trailing `\` continuation marker.
// pasteValue (paths_test.go) interprets those markers.
//
// It also fails the test if a line's payload is wider than width, once
// lipgloss.JoinVertical's trailing-space padding is discounted. JoinVertical
// right-pads every line in a block to the block's widest line; here that
// widest line is the unrelated, unwrapped "<dir> is not on your PATH"
// status line, since dir is deliberately huge in these tests. A terminal
// clips trailing spaces for free, so they never reach a paste.
//
// This check stands in for Bubble Tea's real per-line clip, which this
// in-process View() call does not reproduce on its own.
func rcLineBlock(t *testing.T, view, anchor, indent string, width int) []string {
	t.Helper()
	lines := strings.Split(ansiRE.ReplaceAllString(view, ""), "\n")

	start := -1
	for i, l := range lines {
		if strings.Contains(l, anchor) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("anchor %q not found in view:\n%s", anchor, view)
	}

	var block []string
	for _, l := range lines[start:] {
		if !strings.HasPrefix(l, indent) {
			break
		}
		payload := strings.TrimPrefix(strings.TrimRight(l, " "), indent)
		if len(indent)+len(payload) > width {
			t.Errorf("drawn line's payload is %d chars, wider than the %d-column terminal, so Bubble Tea would clip this: %q", len(indent)+len(payload), width, indent+payload)
		}
		block = append(block, payload)
	}
	return block
}

// The active rc prompt (phaseRC) must show the complete, pasteable rc line
// even on a narrow terminal. Bubble Tea's per-line width clamp must not
// silently clip it. A naive wrap must not break the line inside its quotes.
func TestRCPromptLinePastesSafelyAtNarrowWidths(t *testing.T) {
	for _, width := range []int{60, 80} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := newTestModel(t, "/usr/bin")
			m.dir = longRCDir
			m.width = width

			next, _ := m.Update(installedMsg{path: m.dir + "/stoat"})
			m = next.(Model)
			if m.phase != phaseRC {
				t.Fatalf("phase = %v, want phaseRC", m.phase)
			}

			block := rcLineBlock(t, m.View().Content, "append to "+m.rcPath+":", "    ", width)
			if got := pasteValue(t, m.shell, block); got != m.dir {
				t.Errorf("pasting the rc prompt's line assigns %q, want %q", got, m.dir)
			}
		})
	}
}

// The done screen's "could not write the rc file" branch must show the same
// complete, pasteable line: this is the one case where the rc line is the
// user's only way to finish the PATH change themselves.
func TestDoneRCErrLinePastesSafelyAtNarrowWidths(t *testing.T) {
	for _, width := range []int{60, 80} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := newTestModel(t, "/usr/bin")
			m.dir = longRCDir
			m.phase = phaseDone
			m.binPath = longRCDir + "/stoat"
			m.width = width
			m.rcPath, m.rcLine = ShellRC(m.shell, m.home, m.dir)
			m.rcErr = errors.New("disk full")

			block := rcLineBlock(t, m.View().Content, "add this line yourself:", "          ", width)
			if got := pasteValue(t, m.shell, block); got != m.dir {
				t.Errorf("pasting the recovery line assigns %q, want %q", got, m.dir)
			}
		})
	}
}

// The done screen's declined branch is the other place the line is the
// user's only record of what to add: same requirement, different indent.
func TestDoneDeclinedRCLinePastesSafelyAtNarrowWidths(t *testing.T) {
	for _, width := range []int{60, 80} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := newTestModel(t, "/usr/bin")
			m.dir = longRCDir
			m.phase = phaseDone
			m.binPath = longRCDir + "/stoat"
			m.width = width
			m.rcPath, m.rcLine = ShellRC(m.shell, m.home, m.dir)
			// rcAdded stays false and rcErr stays nil: this is the
			// declined branch, distinct from the rcErr branch above.

			block := rcLineBlock(t, m.View().Content, "add this yourself:", "        ", width)
			if got := pasteValue(t, m.shell, block); got != m.dir {
				t.Errorf("pasting the declined line assigns %q, want %q", got, m.dir)
			}
		})
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

// A hard failure must not swallow the host advice. A user whose install
// died on, say, a permission error, still needs to know what to fix before
// their first VM. This is the only place that guidance is given.
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
// os.TempDir(): the real temp dir is shared with everything else running
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

	// InstallData needs internal/recipes/ to exist in repoDir.
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoDir, "internal", "recipes"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := installCmd(src, t.TempDir(), repoDir, t.TempDir())()
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

	msg := installCmd(src, t.TempDir(), t.TempDir(), t.TempDir())()
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg for a missing src binary, got %T: %+v", msg, msg)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("installCmd left the build temp dir behind on failure: stat err = %v", err)
	}
}
