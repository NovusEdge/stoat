package sshx

import "strings"

// RecipeMarker is the line Provision writes to the log before a recipe's
// output. ParseRecipeMarker reverses it. They are the sole definition of this
// wire format: every reader of the provision log (the TUI step tracker in
// internal/tui/provstep.go, the CLI's --json stage events in
// internal/cli/run_access.go) parses through ParseRecipeMarker, so a change to
// the format here cannot silently desync a reader.
const recipeMarkerPrefix, recipeMarkerSuffix = "=== recipe ", " ==="

// RecipeMarker renders the marker line for a recipe name. Provision wraps the
// result in newlines before writing it.
func RecipeMarker(name string) string {
	return recipeMarkerPrefix + name + recipeMarkerSuffix
}

// ParseRecipeMarker returns the recipe name when line is a marker RecipeMarker
// produced, and ok false otherwise.
//
// The length guard is load-bearing: without it the prefix and suffix overlap on
// "=== recipe ===", which parses as a recipe named "===".
func ParseRecipeMarker(line string) (name string, ok bool) {
	line = strings.TrimSpace(line)
	if len(line) < len(recipeMarkerPrefix)+len(recipeMarkerSuffix) ||
		!strings.HasPrefix(line, recipeMarkerPrefix) || !strings.HasSuffix(line, recipeMarkerSuffix) {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(line, recipeMarkerPrefix), recipeMarkerSuffix)
	return name, name != ""
}
