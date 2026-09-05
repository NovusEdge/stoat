package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAddRecipeRefusesAURL pins the spec's "index names only" rule: a git
// URL, an scp-style remote, a path, or an owner/repo pair are all refused at
// the tool boundary, before core.AddRecipe ever runs.
func TestAddRecipeRefusesAURL(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	for _, ref := range []string{
		"https://github.com/x/stoat-tailscale",
		"git@github.com:x/stoat-tailscale.git",
		"../../etc/passwd",
		"x/y",
	} {
		res := callTool(t, "add_recipe", map[string]any{"name": ref})
		if !res.IsError {
			t.Errorf("add_recipe accepted %q", ref)
			continue
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "invalid recipe name") && !strings.Contains(string(raw), "invalid ref") {
			t.Errorf("add_recipe(%q) refusal did not name the guard: %s", ref, raw)
		}
	}
}

// TestRecipeToolsRefuseUnsafeNames pins that update_recipe and
// remove_recipe take a plain index name. Both reach core with a name only,
// so an @ref is refused rather than dropped without a word.
func TestRecipeToolsRefuseUnsafeNames(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	for _, c := range []struct{ tool, name string }{
		{"update_recipe", "tailscale@v1.2"},
		{"remove_recipe", "../tailscale"},
		{"remove_recipe", "-y"},
		{"remove_recipe", "tailscale@v1.2"},
	} {
		if res := callTool(t, c.tool, map[string]any{"name": c.name}); !res.IsError {
			t.Errorf("%s accepted %q", c.tool, c.name)
		}
	}
}

// TestRemoveRecipeHasNoForce pins the spec's rule that remove_recipe has no
// force parameter: a person, not an agent, removes a recipe a VM still
// uses.
func TestRemoveRecipeHasNoForce(t *testing.T) {
	for _, tool := range listTools(t) {
		if tool.Name != "remove_recipe" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(raw), `"force"`) {
			t.Fatalf("remove_recipe exposes force: %s", raw)
		}
		return
	}
	t.Fatal("remove_recipe is not registered")
}
