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

// bannerArt is drawn above the transcript. It is deliberately empty: when it is
// blank the banner line is skipped and the layout still works, so filling it in
// later costs nothing.
//
// ponytail: stub. Drop a small ASCII mark in here when there is one.
const bannerArt = ""

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

var helpModel = help.New()

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
}

// New builds the model. Every environment value it depends on is a parameter so
// the tests can drive it without touching the real environment.
func New(repoDir, home, shell, pathEnv, prefixEnv string) Model {
	dir := DefaultDir(home, prefixEnv)

	in := textinput.New()
	in.SetValue(dir)
	in.Prompt = ""
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
func (m Model) Failed() bool { return m.err != nil }

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
			return errMsg{err: err}
		}
		return builtMsg{tmpPath: out}
	}
}

func installCmd(src, destDir string) tea.Cmd {
	return func() tea.Msg {
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
		return m, tea.Quit
	}
	if key.Matches(msg, keys.Quit) && m.phase != phaseDir {
		return m, tea.Quit
	}

	switch m.phase {
	case phaseDir:
		if key.Matches(msg, keys.Install) {
			if v := strings.TrimSpace(m.input.Value()); v != "" {
				m.dir = expandHome(v, m.home)
			}
			m.version = Version(m.repoDir)
			m.phase = phaseBuild
			return m, buildCmd(m.repoDir, m.version)
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

	if bannerArt != "" {
		blocks = append(blocks, accentStyle.Render(bannerArt), "")
	}

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
			"  "+helpModel.ShortHelpView([]key.Binding{keys.Install, keys.Interrupt}),
		)

	case phaseBuild:
		return "    " + m.spin.View() + "  building    " + m.version

	case phaseRC:
		return lipgloss.JoinVertical(lipgloss.Left,
			"    "+okStyle.Render("ok")+"    installed   "+m.binPath,
			"",
			"  "+m.dir+" is not on your PATH",
			"  append to "+m.rcPath+":",
			"    "+dimStyle.Render(m.rcLine),
			"",
			"  append it? [Y/n]",
			"  "+helpModel.ShortHelpView([]key.Binding{keys.Accept, keys.Decline}),
		)

	case phaseDone:
		return m.done()
	}
	return ""
}

func (m Model) done() string {
	if m.err != nil {
		return "\n" + errStyle.Render("failed") + " — " + m.err.Error()
	}

	lines := []string{
		"    " + okStyle.Render("ok") + "    installed   " + m.binPath,
		"",
		"done — stoat " + m.version,
	}
	if m.rcAdded {
		lines = append(lines,
			"",
			"  added the PATH line to "+m.rcPath,
			"  open a new shell, or source it, to pick it up",
		)
	}
	if m.rcErr != nil {
		lines = append(lines,
			"",
			"  "+warnStyle.Render("warn")+"  could not write "+m.rcPath+": "+m.rcErr.Error(),
			"        add this line yourself:",
			"          "+dimStyle.Render(m.rcLine),
		)
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
