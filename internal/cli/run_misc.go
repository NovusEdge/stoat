package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}

func runLogs(a *Args, stdout, stderr io.Writer) int {
	if a.Follow && a.JSON {
		return a.failUsage(stdout, stderr, "--follow streams until interrupted and cannot emit one result; use --json without it")
	}
	read := func() ([]byte, error) { return os.ReadFile(logx.Path()) }
	if a.VM != "" {
		read = func() ([]byte, error) {
			rc, err := core.Logs(a.VM, a.Which)
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(rc)
			if closeErr := rc.Close(); err == nil {
				err = closeErr
			}
			return b, err
		}
	} else if err := logx.Init(); err != nil {
		return a.fail(stdout, stderr, err)
	}

	b, err := read()
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.Follow {
		b = completeLines(b)
	}
	lines := splitTail(b, a.N)
	if a.JSON {
		if lines == nil {
			lines = []string{} // never null: a consumer iterates this
		}
		if a.VM == "" {
			return a.ok(stdout, map[string]any{"lines": lines})
		}
		return a.ok(stdout, map[string]any{"vm": a.VM, "which": string(a.Which), "lines": lines})
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	if a.Follow {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := followLog(ctx, read, stdout, len(b), logFollowInterval); err != nil {
			return a.fail(stdout, stderr, err)
		}
		return ExitOK
	}
	if len(lines) == 0 && a.VM != "" {
		fmt.Fprintf(stderr, "no %s log yet for %s\n", a.Which, a.VM)
	}
	return ExitOK
}

const logFollowInterval = 500 * time.Millisecond

// completeLines cuts b after its last newline. Follow emits whole lines only:
// core.Logs redacts secrets per read, and a secret cut in half by a partial
// write would slip past the redaction.
func completeLines(b []byte) []byte {
	return b[:bytes.LastIndexByte(b, '\n')+1]
}

// followLog prints what read returns beyond the first printed bytes, every
// interval, until ctx ends. A shorter read means the file was truncated (the
// apply log restarts on every run), so it starts over from the top.
func followLog(ctx context.Context, read func() ([]byte, error), w io.Writer, printed int, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		b, err := read()
		if err != nil {
			return err
		}
		b = completeLines(b)
		if len(b) < printed {
			printed = 0
		}
		if len(b) > printed {
			if _, err := w.Write(b[printed:]); err != nil {
				return err
			}
			printed = len(b)
		}
	}
}

func splitTail(b []byte, n int) []string {
	if len(b) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// runRecipe implements the recipe subcommands. Authoring a recipe has
// always been "put a correctly named file in the recipes directory": the
// only real problem was that nothing told you so, or what the name had to be.
func runRecipe(a *Args, stdin io.Reader, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "list":
		snapshot, err := recipes.ListSnapshot()
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		out := wire.RecipeList{Roots: make([]wire.RecipeRoot, 0, len(snapshot.Roots)), Recipes: make([]wire.RecipeEntry, 0, len(snapshot.Manifests))}
		for _, root := range snapshot.Roots {
			out.Roots = append(out.Roots, wire.RecipeRoot{Path: root.Path, Scope: root.Scope})
		}
		for _, m := range snapshot.Manifests {
			scope := snapshot.Scopes[m.Name]
			e := wire.RecipeEntry{Name: m.Name, Description: m.Description, Scope: scope}
			if pin, ok := snapshot.Pins[m.Name]; ok {
				e.Source, e.Ref, e.Commit = pin.Source, pin.Ref, short(pin.Commit)
			}
			out.Recipes = append(out.Recipes, e)
		}
		out.Roots, out.Recipes = wire.NonNil(out.Roots), wire.NonNil(out.Recipes)
		if a.JSON {
			return a.ok(stdout, out)
		}
		fmt.Fprintf(stdout, "%-20s %-9s %-8s %s\n", "NAME", "SCOPE", "COMMIT", "DESCRIPTION")
		if len(out.Recipes) == 0 {
			fmt.Fprintln(stdout, "  (none)")
			return ExitOK
		}
		for _, e := range out.Recipes {
			fmt.Fprintf(stdout, "%-20s %-9s %-8s %s\n", e.Name, e.Scope, e.Commit, e.Description)
		}
		return ExitOK

	case "add":
		return runRecipeAdd(a, stdin, stdout, stderr)
	case "lock":
		return runRecipeLock(a, stdout, stderr)
	case "sync":
		return runRecipeSync(a, stdout, stderr)
	case "update":
		return runRecipeUpdate(a, stdout, stderr)
	case "rm":
		return runRecipeRM(a, stdin, stdout, stderr)
	case "search":
		return runRecipeSearch(a, stdout, stderr)
	case "refresh":
		return runRecipeRefresh(a, stdout, stderr)

	case "new":
		path, err := recipes.New(a.VM, a.OS, a.Backend)
		if err != nil {
			return a.fail(stdout, stderr, err)
		}
		if a.JSON {
			return a.ok(stdout, map[string]any{"path": path})
		}
		fmt.Fprintln(stdout, path)
		return ExitOK

	case "show":
		return runRecipeShow(a, stdout, stderr)
	}
	// Unreachable: Parse rejects any action but the declared recipe commands.
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError("recipe: unknown action "+a.Sub))
		return ExitUsage
	}
	fmt.Fprintln(stderr, "stoat: recipe: unknown action", a.Sub)
	return ExitUsage
}
