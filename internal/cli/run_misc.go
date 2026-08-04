package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

// colorEnabled reports whether ANSI color may be used on stdout: honouring
// NO_COLOR and disabling automatically when stdout is not a terminal, so
// piped output (`stoat ls | awk ...`) never carries escape codes.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// colorState pads to width FIRST, then wraps in escapes: the codes are
// zero-width on screen but count toward %-8s, so colouring before padding
// silently eats 9 columns and skews every row after STATE.
func colorState(state string, width int) string {
	return colorize(fmt.Sprintf("%-*s", width, state), state)
}

func colorize(padded, state string) string {
	if !colorEnabled() {
		return padded
	}
	switch state {
	case "running":
		return "\x1b[32m" + padded + "\x1b[0m" // green
	case "broken":
		return "\x1b[31m" + padded + "\x1b[0m" // red
	default:
		return padded
	}
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}

func runLogs(a *Args, stdout, stderr io.Writer) int {
	if err := logx.Init(); err != nil {
		return a.fail(stdout, stderr, err)
	}
	lines, err := tailLines(logx.Path(), a.N)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		if lines == nil {
			lines = []string{} // never null: a consumer iterates this
		}
		return a.ok(stdout, map[string]any{"lines": lines})
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	return ExitOK
}

func tailLines(path string, n int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// runRecipe implements "recipe list" and "recipe new". Authoring a recipe has
// always been "put a correctly named file in the recipes directory": the
// only real problem was that nothing told you so, or what the name had to be.
func runRecipe(a *Args, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "list":
		names, err := recipes.Installed()
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			if names == nil {
				names = []string{}
			}
			return a.ok(stdout, map[string]any{"dir": recipes.Dir(), "recipes": names})
		}
		fmt.Fprintln(stdout, recipes.Dir())
		if len(names) == 0 {
			fmt.Fprintln(stdout, "  (none)")
			return ExitOK
		}
		for _, n := range names {
			fmt.Fprintln(stdout, "  "+n)
		}
		return ExitOK

	case "new":
		path, err := recipes.New(a.VM, a.OS, a.Backend)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			return a.ok(stdout, map[string]any{"path": path})
		}
		fmt.Fprintln(stdout, path)
		if !a.Quiet {
			fmt.Fprintln(stdout, "edit it, then pick it in the new-vm form for a matching vm")
		}
		return ExitOK
	}
	// Unreachable: Parse rejects any action but list/new.
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError("recipe: unknown action "+a.Sub))
		return ExitUsage
	}
	fmt.Fprintln(stderr, "stoat: recipe: unknown action", a.Sub)
	return ExitUsage
}
