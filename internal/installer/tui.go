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

// defaultWidth applies until the first WindowSizeMsg arrives. It also floors
// a terminal that reports an unusably narrow width.
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

// keys use key.Binding, not raw string comparison. This is the Bubbles idiom.
// It also survives the v2 migration: v2 renamed space from " " to "space".
// key.Matches keeps working; a `case " ":` would silently break.
//
// Quit is separate from Interrupt. "q" is a character at the dir prompt, so
// it can only quit outside phaseDir. That needs its own key.Matches call,
// not a raw msg.String() check on a combined binding.
var keys = struct {
	Install, Accept, Decline, Interrupt, Quit key.Binding
}{
	Install:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "install here")),
	Accept:    key.NewBinding(key.WithKeys("y", "Y", "enter"), key.WithHelp("y", "append it")),
	Decline:   key.NewBinding(key.WithKeys("n", "N"), key.WithHelp("n", "skip")),
	Interrupt: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
}

// helpModel returns a fresh help.Model instead of a shared package-level
// value. ShortHelpView never mutates it today, but a mutable package-level
// UI model is the kind of shared state that breaks later. help.New() is a
// small struct literal; building one per render costs nothing.
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

// Model is the whole installer. It has no separate screens.
// It is one transcript that grows, with the active prompt at the bottom.
// The output survives in the user's scrollback after the installer exits.
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
	// rcErr is a failed rc write. It stays separate from err. By the time
	// AppendRC runs, the build and install already succeeded, so the user
	// can finish the rc write by hand: this failure does not fail the whole
	// run. See Failed and done.
	rcErr error
	err   error
	width int
	// cancelled is set when ctrl+c or q exits before the run reaches
	// phaseDone on its own. See Failed and done: it counts as a failure
	// only if binPath was still empty at that point.
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

// Failed reports whether the installer stopped on an error, so main can pick
// an exit code.
//
// A failed rc write does not count. By the time AppendRC can fail, the
// binary is already built and installed, so the installer exits 0 with a
// warning instead of reporting a hard failure.
//
// Quitting before binPath is set is also a failure: nothing was installed,
// and `just setup` would report success falsely. Quitting after binPath is
// set is not a failure. The install already succeeded, and walking away
// from the PATH question is the same outcome as answering it "n".
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
			// Nothing will call installCmd to clean this up. The build
			// produced no binary to hand it, so this path
			// removes the temp dir itself.
			_ = os.RemoveAll(tmp)
			return errMsg{err: err}
		}
		return builtMsg{tmpPath: out}
	}
}

// installCmd copies the built binary into place. It creates no data root:
// every stoat command runs config.EnsureRoot, recipes.Install and keys.Ensure
// first, and those honour STOAT_HOME.
func installCmd(src, destDir string) tea.Cmd {
	return func() tea.Msg {
		// buildCmd's temp dir has done its job once the binary is copied out,
		// whether the copy succeeds or fails. It is removed here
		// unconditionally, so a run never leaks ~10MB.
		defer func() { _ = os.RemoveAll(filepath.Dir(src)) }()
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
		// Only phaseChecks and phaseBuild render the spinner. Ticking
		// through phaseDir and phaseRC would repaint the whole transcript
		// ~10x/s for a frame nothing shows. The chain dies here; phaseBuild's
		// dispatch in key() restarts it explicitly.
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
	// into; at the dir prompt it is a character. It is checked separately,
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
			// Abs runs whether the value came from the prompt or the
			// untouched default. It turns a bare "bin" into a real path,
			// not a PATH entry any cwd can shadow. paths.go's quoting
			// later trusts this value to already be a real filesystem path.
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
			// A failed write here is not fatal (see the rcErr field comment),
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

// cancel is ctrl+c or q leaving before the run reaches phaseDone on its own.
// It still needs a phase to render the final frame, so it goes to phaseDone
// like every other terminal state. Failed and done tell an abort apart from
// a normal finish using m.cancelled and m.binPath.
func (m Model) cancel() (tea.Model, tea.Cmd) {
	m.cancelled = true
	m.phase = phaseDone
	return m, tea.Quit
}

// rcLineLines renders m.rcLine indented by indent, broken across physical
// lines (see WrapRCLine) so Bubble Tea's per-line width clamp never clips
// it. indent repeats on every line, continuation lines included. A shell
// reading a `\`-continued paste treats leading whitespace before the
// reopened quote as ordinary inter-token whitespace, not part of the
// quoted value. The indent is invisible in what gets pasted.
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
// lipgloss/table sizes the columns, so no width is hardcoded. It measures
// cells ANSI-aware, so pre-colored status cells still line up. A hand-rolled
// pad using len() would get this wrong for any non-ASCII detail string.
//
// BorderBottom(false) with no header matches a bug internal/tui's fields.go
// documents in lipgloss v2.0.5's table: computeHeight() undercounts a
// headerless table by one, and Render() clamps to min(t.height,
// computeHeight()). The bug stays silent here only because this table never
// calls .Height(): t.height stays 0, so the clamp is skipped (lipgloss
// treats MaxHeight(0) as unset). The moment something calls .Height() on
// this table, the last row (/dev/kvm, most often) disappears. See
// fields.go's BorderBottom(true) comment for the full mechanism.
func (m Model) checkTable() string {
	t := table.New().
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).BorderHeader(false).
		StyleFunc(func(_, _ int) lipgloss.Style { return cellStyle })

	for _, c := range m.checks {
		t.Row("   "+status(c), checkLabel(c), c.Detail)
	}
	return t.Render()
}

// repairProblems is the display subset of failed checks. hostcheck.Problems
// intentionally excludes optional failures for readiness aggregation, while
// the installer must still tell the user how to repair every missing tool.
func repairProblems(cs []Check) []Check {
	var out []Check
	for _, c := range cs {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

func checkLabel(c Check) string {
	if c.Optional {
		return c.Name + " (optional)"
	}
	return c.Name
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
	// A hard failure still gets the host advice below. The checks already
	// ran by the time anything can fail (build, install, or a bad dir). A
	// user whose install died on, say, a permission error still needs to
	// know what to fix before their first VM. This guidance always prints,
	// not only on a clean run.
	var lines []string
	switch {
	case m.cancelled && m.binPath == "":
		// Left before the build or install finished. Unlike every other
		// branch here, nothing was installed. Failed() keys on the same fact.
		lines = []string{"", errStyle.Render("cancelled") + ": nothing was installed"}
	case m.err != nil:
		lines = []string{"", errStyle.Render("failed") + ": " + m.err.Error()}
	default:
		lines = []string{
			"    " + okStyle.Render("ok") + "    installed   " + m.binPath,
			"",
			"done: stoat " + m.version,
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
			// Declined. rcLine is set only once the rc prompt has shown (see
			// the installedMsg case in Update), so this branch is reachable
			// only after a real "n". It mirrors the failed-write branch
			// above: a user who skipped it still needs the line to add by
			// hand.
			lines = append(lines,
				"",
				"  skipped, add this yourself:",
			)
			lines = append(lines, m.rcLineLines("        ")...)
		}
	}

	if problems := repairProblems(m.checks); len(problems) > 0 {
		lines = append(lines, "", "before your first VM:")
		seen := map[string]bool{}
		for _, c := range problems {
			lines = append(lines, "", "  "+warnStyle.Render(checkLabel(c))+": "+c.Detail)
			for _, f := range c.Fix {
				// Fixes are deduplicated here, not in Check. Two checks
				// (qemu-img, qemu-system-x86_64) can share one package. The
				// display layer is where "don't repeat the same line twice"
				// belongs.
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
