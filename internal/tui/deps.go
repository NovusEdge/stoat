package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/novusedge/stoat/internal/core"
)

// resolveDeps wraps core.ResolveDependencies for the create form and the
// edit screen's recipe checkboxes. The "recipe not applicable: " prefix
// core.ErrRecipeNotApplicable carries is meant for CLI output stacked with
// other errors; the TUI's toast already frames the message as an error, so
// the prefix is redundant here.
func resolveDeps(osName string, selected []string) ([]core.DepAddition, error) {
	added, err := core.ResolveDependencies(osName, selected)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), "recipe not applicable: "))
	}
	return added, nil
}

// depMessage renders ResolveDependencies' additions as the toast text a
// recipe checkbox toggle shows. Checking a recipe can pull in more than one
// dependency transitively, so every addition is reported, not just the
// first.
func depMessage(added []core.DepAddition) string {
	parts := make([]string, len(added))
	for i, a := range added {
		parts[i] = fmt.Sprintf("Added %s (required by %s)", a.Recipe, a.RequiredBy)
	}
	return strings.Join(parts, "; ")
}

// selectedNames is the recipe names currently checked in sel, in names'
// order. Both formModel and editModel key their selection off the recipe
// name rather than index, so this helper works for either.
func selectedNames(names []string, sel map[string]bool) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if sel[n] {
			out = append(out, n)
		}
	}
	return out
}
