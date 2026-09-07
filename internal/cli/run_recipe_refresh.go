package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/recipes"
)

// runRecipeRefresh is the explicit recovery for a bundled recipe file whose
// manifest entry went missing by some means other than a hand edit stoat
// meant to protect: Install then treats it as edited forever and never
// refreshes it again.
func runRecipeRefresh(a *Args, stdout, stderr io.Writer) int {
	results, err := recipes.Refresh(a.Names)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	rows := make([]wire.RecipeRefreshed, 0, len(results))
	for _, r := range results {
		rows = append(rows, wire.RecipeRefreshed{Path: r.Path, Status: string(r.Status)})
	}
	if a.JSON {
		return a.ok(stdout, wire.RecipeRefresh{Files: wire.NonNil(rows)})
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%-10s %s\n", row.Status, row.Path)
	}
	return ExitOK
}
