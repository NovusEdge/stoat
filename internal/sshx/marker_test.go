package sshx

import "testing"

func TestRecipeMarkerRoundTrips(t *testing.T) {
	for _, name := range []string{"xfce", "a.sh", "devtools.alpine.sh"} {
		got, ok := ParseRecipeMarker(RecipeMarker(name))
		if !ok || got != name {
			t.Errorf("ParseRecipeMarker(RecipeMarker(%q)) = %q, %v; want %q, true", name, got, ok, name)
		}
	}
}

func TestParseRecipeMarkerRejectsNonMarkers(t *testing.T) {
	// "=== recipe ===" is the overlap case the length guard exists for: without
	// it, prefix and suffix parse it as a recipe named "===".
	for _, line := range []string{"", "=== recipe ===", "=== recipe foo", "recipe foo ===", "some output"} {
		if name, ok := ParseRecipeMarker(line); ok {
			t.Errorf("ParseRecipeMarker(%q) matched as %q, want no match", line, name)
		}
	}
}
