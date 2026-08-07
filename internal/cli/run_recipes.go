package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
)

// recipesFor answers `stoat recipes` for whatever axes a.OS/a.Backend leave
// unfiltered. core.RecipeFilter requires BOTH fields (there is no "every OS"
// or "every backend" scope in recipes.List either), so an empty axis here is
// resolved to every guest.OS it could mean and the per-OS results are merged,
// rather than passed through and rejected.
//
//   - both given: one core.Recipes call, unfiltered.
//   - OS given, backend empty: that OS's own backend (guest.Lookup), so
//     `--os alpine` means "what alpine gets", not an error.
//   - backend given, OS empty: every known OS queried against that backend.
//   - neither given: every known OS queried against its own backend, i.e.
//     the full catalog.
//
// An OS named on the command line that guest.Lookup does not know is not
// silently dropped: it is passed through as-is with an empty backend, so
// core.Recipes's own "needs both OS and Backend" error surfaces instead of
// stoat guessing what an unknown OS's backend would be.
func recipesFor(osName, backend string) ([]core.Recipe, error) {
	type pair struct{ os, backend string }
	var pairs []pair
	switch {
	case osName != "" && backend != "":
		pairs = []pair{{osName, backend}}
	case osName != "":
		b := backend
		if g, ok := guest.Lookup(osName); ok {
			b = g.Backend
		}
		pairs = []pair{{osName, b}}
	case backend != "":
		for _, g := range guest.All() {
			pairs = append(pairs, pair{g.Name, backend})
		}
	default:
		for _, g := range guest.All() {
			pairs = append(pairs, pair{g.Name, g.Backend})
		}
	}

	seen := map[string]bool{}
	var out []core.Recipe
	for _, p := range pairs {
		rs, err := core.Recipes(core.RecipeFilter{OS: p.os, Backend: p.backend})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if seen[r.Name] {
				continue
			}
			seen[r.Name] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// runRecipes lists recipes applicable to a.OS/a.Backend, either of which may
// be empty (see recipesFor).
func runRecipes(a *Args, stdout, stderr io.Writer) int {
	rs, err := recipesFor(a.OS, a.Backend)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"recipes": wire.FromRecipes(rs)})
	}
	if len(rs) == 0 {
		fmt.Fprintln(stdout, "no recipes")
		return ExitOK
	}
	fmt.Fprintf(stdout, "%-30s %s\n", "NAME", "DESCRIPTION")
	for _, r := range rs {
		fmt.Fprintf(stdout, "%-30s %s\n", r.Name, r.Description)
	}
	return ExitOK
}

// runCheckRecipes reports, for each named recipe, why it would not apply to
// a.OS/a.Backend; an empty issues list means every one of them would.
//
// Exit is 0 either way, matching runDoctor: this command SUCCEEDED at
// checking, and an inapplicable recipe is the answer it was asked for, not a
// failure to produce one. Only core.CheckRecipes returning an error is a
// real failure.
func runCheckRecipes(a *Args, stdout, stderr io.Writer) int {
	issues, err := core.CheckRecipes(a.OS, a.Backend, a.Only)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"applicable": len(issues) == 0,
			"issues":     wire.FromRecipeIssues(issues),
		})
	}
	if len(issues) == 0 {
		fmt.Fprintln(stdout, "all applicable")
		return ExitOK
	}
	for _, i := range issues {
		fmt.Fprintf(stdout, "%s: %s\n", i.Name, i.Reason)
	}
	return ExitOK
}
