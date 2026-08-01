package installer

import (
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"

	"github.com/novusedge/stoat/internal/theme"
)

// defaultWidth is used until the first WindowSizeMsg arrives, and as the floor
// for a terminal that reports something unusably narrow.
const defaultWidth = 60

var (
	okStyle     = lipgloss.NewStyle().Foreground(theme.Up)
	warnStyle   = lipgloss.NewStyle().Foreground(theme.Warn)
	errStyle    = lipgloss.NewStyle().Foreground(theme.Err)
	accentStyle = lipgloss.NewStyle().Foreground(theme.Accent)
	dimStyle    = lipgloss.NewStyle().Foreground(theme.Dim)

	// cellStyle spaces the check table's columns. lipgloss/table measures cells
	// ANSI-aware, so pre-colored status cells still align.
	cellStyle = lipgloss.NewStyle().PaddingRight(2)
)

// keys are declared as key.Bindings rather than compared as strings, which is
// both the Bubbles idiom and the thing that survives the v2 migration: v2
// renames space from " " to "space", and key.Matches goes on working while a
// `case " ":` silently stops.
//
// Quit is split from Interrupt rather than one binding covering both "ctrl+c"
// and "q": "q" is a character at the dir prompt, so it can only quit outside
// phaseDir, and that has to be a second key.Matches call rather than a raw
// msg.String() check on the combined binding.
var keys = struct {
	Install, Accept, Decline, Interrupt, Quit key.Binding
}{
	Install:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "install here")),
	Accept:    key.NewBinding(key.WithKeys("y", "Y", "enter"), key.WithHelp("y", "append it")),
	Decline:   key.NewBinding(key.WithKeys("n", "N"), key.WithHelp("n", "skip")),
	Interrupt: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
}

// helpModel returns a fresh help.Model rather than sharing one package-level
// value: ShortHelpView never mutates it in practice, but a mutable
// package-level UI model is exactly the kind of shared state that looks
// harmless right up until something does. help.New() is a small struct
// literal, so building one per render costs nothing.
func helpModel() help.Model { return help.New() }

type phase int

const (
	phaseChecks phase = iota
	phaseDir
	phaseBuild
	phaseRC
	phaseDone
)

type (
	checksDoneMsg struct{ checks []Check }
	builtMsg      struct{ tmpPath string }
	installedMsg  struct{ path string }
	errMsg        struct{ err error }
)

// Model is the whole installer. There are no screens: it is one transcript that
// grows, with whatever prompt is active at the bottom, so the output survives in
// the user's scrollback after it exits.
type Model struct {
	phase   phase
	checks  []Check
	input   textinput.Model
	spin    spinner.Model
	repoDir string
	home    string
	shell   string
	pathEnv string
	dir     string
	version string
	binPath string
	rcPath  string
	rcLine  string
	rcAdded bool
	// rcErr is a failed rc write. It is kept apart from err deliberately: by
	// the time AppendRC runs, the build and install already succeeded, so
	// this is a recoverable failure the user can finish by hand — not a
	// reason to report the whole run as failed. See Failed and done.
	rcErr error
	err   error
	width int
	// cancelled is set by ctrl+c/q leaving before the run reached its own
	// completion (phaseDone via the normal message flow). See Failed and
	// done: whether that counts as a failure depends on whether binPath was
	// already set when it happened.
	cancelled bool
}

// New builds the model. Every environment value it depends on is a parameter so
// the tests can drive it without touching the real environment.
func New(repoDir, home, shell, pathEnv, prefixEnv string) Model {
	dir := DefaultDir(home, prefixEnv)

	in := theme.TextInput()
	in.SetValue(dir)
	in.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = accentStyle

	return Model{
		phase:   phaseChecks,
		input:   in,
		spin:    sp,
		repoDir: repoDir,
		home:    home,
		shell:   shell,
		pathEnv: pathEnv,
		dir:     dir,
		width:   defaultWidth,
	}
}

// Failed reports whether the installer stopped on an error, so main can pick an
// exit code. A failed rc write does not count: the binary is already built and
// installed by the time that can happen, so it exits 0 with a warning rather
// than reporting a hard failure over a recoverable one.
//
// Quitting before binPath is set is also a failure: nothing was installed, so
// `just setup` reporting success would be a lie. Quitting after binPath is
// set is not -- the install already succeeded, and walking away from the
// optional PATH question is the same non-fatal outcome as answering it "n".
func (m Model) Failed() bool { return m.err != nil || (m.cancelled && m.binPath == "") }

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, runChecksCmd())
}

func runChecksCmd() tea.Cmd {
	return func() tea.Msg { return checksDoneMsg{checks: RunChecks(DetectDistro())} }
}

func buildCmd(repoDir, version string) tea.Cmd {
	return func() tea.Msg {
		tmp, err := os.MkdirTemp("", "stoat-build-*")
		if err != nil {
			return errMsg{err: err}
		}
		out := filepath.Join(tmp, "stoat")
		if err := Build(repoDir, version, out); err != nil {
			// Nothing will ever call installCmd to clean this up -- the build
			// never produced a binary to hand it -- so this is the one path
			// that has to remove the temp dir itself.
			os.RemoveAll(tmp)
			return errMsg{err: err}
		}
		return builtMsg{tmpPath: out}
	}
}

func installCmd(src, destDir string) tea.Cmd {
	return func() tea.Msg {
		// The temp dir buildCmd created is done its job the moment the binary
		// is copied out of it -- whether that copy succeeds or fails -- so it
		// goes away here unconditionally rather than leaking ~10MB per run.
		defer os.RemoveAll(filepath.Dir(src))
		path, err := Install(src, destDir)
		if err != nil {
			return errMsg{err: err}
		}
		return installedMsg{path: path}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case errMsg:
		m.err = msg.err
		m.phase = phaseDone
		return m, tea.Quit

	case checksDoneMsg:
		m.checks = msg.checks
		m.phase = phaseDir
		return m, nil

	case builtMsg:
		return m, installCmd(msg.tmpPath, m.dir)

	case installedMsg:
		m.binPath = msg.path
		if OnPath(m.dir, m.pathEnv) {
			m.phase = phaseDone
			return m, tea.Quit
		}
		m.rcPath, m.rcLine = ShellRC(m.shell, m.home, m.dir)
		m.phase = phaseRC
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		if m.width < defaultWidth {
			m.width = defaultWidth
		}
		return m, nil

	case spinner.TickMsg:
		// Only phaseChecks and phaseBuild ever render the spinner. Ticking on
		// through phaseDir and phaseRC would repaint the whole transcript
		// ~10x/s for a frame nothing shows, so the chain is left to die here
		// and phaseBuild's dispatch in key() restarts it explicitly.
		if m.phase != phaseChecks && m.phase != phaseBuild {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits. "q" only quits once there is nothing left to type
	// into — at the dir prompt it is a character — so it is checked separately
	// and only outside phaseDir.
	if key.Matches(msg, keys.Interrupt) {
		return m.cancel()
	}
	if key.Matches(msg, keys.Quit) && m.phase != phaseDir {
		return m.cancel()
	}

	switch m.phase {
	case phaseDir:
		if key.Matches(msg, keys.Install) {
			dir := m.dir
			if v := strings.TrimSpace(m.input.Value()); v != "" {
				dir = expandHome(v, m.home)
			}
			// Abs runs whether the value came from the prompt or the untouched
			// default: it is what turns a bare "bin" into a real path instead
			// of a PATH entry any cwd can shadow, and it is what paths.go's
			// quoting later trusts to already be a real filesystem path.
			abs, err := filepath.Abs(dir)
			if err != nil {
				m.err = err
				m.phase = phaseDone
				return m, tea.Quit
			}
			m.dir = abs
			m.version = Version(m.repoDir)
			m.phase = phaseBuild
			// The tick chain died when checks landed (see the TickMsg case in
			// Update); phaseBuild renders the spinner again, so it has to be
			// restarted here.
			return m, tea.Batch(buildCmd(m.repoDir, m.version), m.spin.Tick)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case phaseRC:
		// Decline is checked first: Accept binds "enter" too, and a user who
		// typed "n" must never have it read as yes.
		switch {
		case key.Matches(msg, keys.Decline):
			m.phase = phaseDone
			return m, tea.Quit
		case key.Matches(msg, keys.Accept):
			// A failed write here is not fatal — see the rcErr field comment —
			// so it goes to rcErr, not err.
			added, err := AppendRC(m.rcPath, m.rcLine)
			m.rcAdded = added
			m.rcErr = err
			m.phase = phaseDone
			return m, tea.Quit
		}
	}
	return m, nil
}

// cancel is ctrl+c/q leaving before the run reached phaseDone on its own.
// It still needs a phase to render the final frame from, so it goes to
// phaseDone like every other terminal state; Failed and done tell an abort
// apart from a normal finish by m.cancelled and m.binPath.
func (m Model) cancel() (tea.Model, tea.Cmd) {
	m.cancelled = true
	m.phase = phaseDone
	return m, tea.Quit
}

// rcLineLines renders m.rcLine indented by indent, breaking it across
// physical lines (see WrapRCLine) so it never gets clipped by Bubble Tea's
// per-line width clamp. indent is repeated on every line, continuation
// lines included: a shell reading a `\`-continued paste treats leading
// whitespace before the reopened quote as ordinary inter-token whitespace,
// not part of the quoted value, so this is invisible to what gets pasted.
func (m Model) rcLineLines(indent string) []string {
	chunks := WrapRCLine(m.shell, m.dir, m.width-len(indent))
	lines := make([]string, len(chunks))
	for i, c := range chunks {
		lines[i] = indent + dimStyle.Render(c)
	}
	return lines
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func (m Model) View() tea.View {
	var blocks []string

	blocks = append(blocks, accentStyle.Render(theme.BannerArt), "")

	if len(m.checks) > 0 {
		blocks = append(blocks,
			"  checking host",
			m.checkTable(),
			"",
			dimStyle.Render("  "+strings.Repeat("─", m.ruleWidth())),
			"",
		)
	}

	s := lipgloss.JoinVertical(lipgloss.Left, append(blocks, m.active())...) + "\n"
	return tea.NewView(s)
}

// checkTable renders the probe results as an aligned table.
//
// lipgloss/table does the column sizing, so no width is hardcoded and the
// pre-colored status cells still line up -- it measures cells ANSI-aware, which
// a hand-rolled pad using len() would get wrong the moment a detail string
// contains anything non-ASCII.
//
// BorderBottom(false) with no header is the exact combination internal/tui's
// fields.go documents as buggy in lipgloss v2.0.5's table: computeHeight()
// undercounts a headerless table by one, and Render() clamps to
// min(t.height, computeHeight()). It's silent here only because this table
// never calls .Height(), so t.height stays 0 and the clamp is skipped
// (lipgloss treats MaxHeight(0) as unset). The moment something calls
// .Height() on this table, the last row — /dev/kvm, most often — disappears.
// See fields.go's BorderBottom(true) comment for the full mechanism.
func (m Model) checkTable() string {
	t := table.New().
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		StyleFunc(func(_, _ int) lipgloss.Style { return cellStyle })

	for _, c := range m.checks {
		t.Row("   "+status(c), c.Name, c.Detail)
	}
	return t.Render()
}

func (m Model) ruleWidth() int {
	w := m.width - 4
	if w > 60 {
		w = 60
	}
	if w < 10 {
		w = 10
	}
	return w
}

// active is whatever the installer is doing or asking right now: the live
// bottom of the transcript. Everything above it is already settled.
func (m Model) active() string {
	switch m.phase {
	case phaseChecks:
		return "  " + m.spin.View() + " checking host"

	case phaseDir:
		return lipgloss.JoinVertical(lipgloss.Left,
			"  install to: "+m.input.View(),
			"",
			"  "+helpModel().ShortHelpView([]key.Binding{keys.Install, keys.Interrupt}),
		)

	case phaseBuild:
		return "    " + m.spin.View() + "  building    " + m.version

	case phaseRC:
		lines := []string{
			"    " + okStyle.Render("ok") + "    installed   " + m.binPath,
			"",
			"  " + m.dir + " is not on your PATH",
			"  append to " + m.rcPath + ":",
		}
		lines = append(lines, m.rcLineLines("    ")...)
		lines = append(lines,
			"",
			"  append it? [Y/n]",
			"  "+helpModel().ShortHelpView([]key.Binding{keys.Accept, keys.Decline}),
		)
		return lipgloss.JoinVertical(lipgloss.Left, lines...)

	case phaseDone:
		return m.done()
	}
	return ""
}

func (m Model) done() string {
	// A hard failure still gets the host advice below: the checks already ran
	// by the time anything can fail (build, install, or a bad dir), and a
	// user whose install died on e.g. a permission error still needs to know
	// what to fix before their first VM -- that guidance is meant to be given
	// once, not only on a clean run.
	var lines []string
	switch {
	case m.cancelled && m.binPath == "":
		// Left before the build/install ever finished: unlike every other
		// branch here, nothing was actually installed, which is exactly what
		// Failed() keys on too.
		lines = []string{"", errStyle.Render("cancelled") + " — nothing was installed"}
	case m.err != nil:
		lines = []string{"", errStyle.Render("failed") + " — " + m.err.Error()}
	default:
		lines = []string{
			"    " + okStyle.Render("ok") + "    installed   " + m.binPath,
			"",
			"done — stoat " + m.version,
		}
		switch {
		case m.rcAdded:
			lines = append(lines,
				"",
				"  added the PATH line to "+m.rcPath,
				"  open a new shell, or source it, to pick it up",
			)
		case m.rcErr != nil:
			lines = append(lines,
				"",
				"  "+warnStyle.Render("warn")+"  could not write "+m.rcPath+": "+m.rcErr.Error(),
				"        add this line yourself:",
			)
			lines = append(lines, m.rcLineLines("          ")...)
		case m.rcLine != "":
			// Declined: rcLine is only ever set once the rc prompt has been
			// shown (see the installedMsg case in Update), so this is
			// reachable only after a real "n". Mirrors the failed-write
			// branch above, which already got this right -- a user who
			// skipped it still needs the line to add by hand.
			lines = append(lines,
				"",
				"  skipped — add this yourself:",
			)
			lines = append(lines, m.rcLineLines("        ")...)
		}
	}

	if problems := Problems(m.checks); len(problems) > 0 {
		lines = append(lines, "", "before your first VM:")
		seen := map[string]bool{}
		for _, c := range problems {
			lines = append(lines, "", "  "+warnStyle.Render(c.Name)+" — "+c.Detail)
			for _, f := range c.Fix {
				// Fixes are deduplicated here rather than in Check: two checks
				// (qemu-img, qemu-system-x86_64) can share one package, and the
				// display layer is where "don't repeat the same line twice" belongs.
				if seen[f] {
					continue
				}
				seen[f] = true
				lines = append(lines, "    "+dimStyle.Render(f))
			}
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func status(c Check) string {
	if c.OK {
		return okStyle.Render("ok")
	}
	return warnStyle.Render("warn")
}
