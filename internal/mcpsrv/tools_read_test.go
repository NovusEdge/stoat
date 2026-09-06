package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestListGuestsReturnsTheBundledSet(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "list_guests", map[string]any{})
	if res.IsError {
		t.Fatalf("list_guests failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	for _, name := range []string{"alpine", "debian", "ubuntu", "fedora", "arch"} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Errorf("list_guests omitted %q: %s", name, raw)
		}
	}
}

func TestGuestInfoReturnsBundledFields(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "guest_info", map[string]any{"name": "alpine"})
	if res.IsError {
		t.Fatalf("guest_info failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Init string `json:"init"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Init != "openrc" {
		t.Errorf("guest_info(alpine) init = %q, want openrc: %s", out.Init, raw)
	}
}

func TestGuestInfoNamesAnUnknownGuest(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "guest_info", map[string]any{"name": "plan9"})
	if !res.IsError {
		t.Fatal("guest_info accepted an unknown guest")
	}
}

func TestRecipeSchemaListsParams(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "recipe_schema", map[string]any{"name": "docker"})
	if res.IsError {
		t.Fatalf("recipe_schema failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Params []struct {
			Name        string `json:"name"`
			DefaultFrom string `json:"default_from"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range out.Params {
		if p.Name != "user" {
			continue
		}
		found = true
		if p.DefaultFrom != "ssh_user" {
			t.Fatalf("recipe_schema(docker) user.default_from = %q, want ssh_user", p.DefaultFrom)
		}
	}
	if !found {
		t.Fatalf("recipe_schema(docker) params missing user: %s", raw)
	}
}

func TestRecipeSchemaOmitsDefaultFromWhenUndeclared(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "recipe_schema", map[string]any{"name": "python-dev"})
	if res.IsError {
		t.Fatalf("recipe_schema failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Params []map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range out.Params {
		if p["name"] != "venv_dir" {
			continue
		}
		found = true
		if _, ok := p["default_from"]; ok {
			t.Fatalf("recipe_schema(python-dev) venv_dir carries default_from unset by the manifest: %s", raw)
		}
	}
	if !found {
		t.Fatalf("recipe_schema(python-dev) params missing venv_dir: %s", raw)
	}
}

func TestSearchRecipesRefusesAFlagTerm(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if res := callTool(t, "search_recipes", map[string]any{"term": "--refresh"}); !res.IsError {
		t.Fatal("search_recipes accepted a term that reads as a flag")
	}
}

func TestVMStatusReportsRecipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	res := callTool(t, "vm_status", map[string]any{"vm": "dev"})
	if res.IsError {
		t.Fatalf("vm_status failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		RecipeStates []any `json:"recipes_detail"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.RecipeStates == nil {
		t.Fatalf("recipes_detail is null, want an empty list: %s", raw)
	}
}
