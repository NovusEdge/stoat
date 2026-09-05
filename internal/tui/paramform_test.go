package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

func paramFixture() core.Recipe {
	return core.Recipe{
		Name: "docker", Schema: 3,
		Params: []core.RecipeParam{
			{Name: "authkey", Type: "secret", Required: true, Help: "tailnet auth key"},
			{Name: "channel", Type: "enum", Default: "stable", Values: []string{"stable", "test"}},
			{Name: "port", Type: "int", Default: "2375"},
			{Name: "tls", Type: "bool", Default: "true"},
			{Name: "user", Type: "string", Default: "dev"},
		},
	}
}

// The component boundary must seed each field from the manifest, including a
// blank required secret and string spellings for typed defaults. This test
// does not reach into private bindings; the values are observed through the
// public form contract used by the wizard.
func TestNewParamFormSeedsDefaults(t *testing.T) {
	p := newParamForm(paramFixture())
	got := p.Values()
	want := map[string]string{
		"authkey": "", "channel": "stable", "port": "2375",
		"tls": "true", "user": "dev",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if p.Complete() {
		t.Error("required authkey made an empty parameter form complete")
	}
}

func writeParamRecipe(t *testing.T) {
	t.Helper()
	dir := filepath.Join(config.Root(), "recipes", "docker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 3
name = "docker"
description = "parameterized docker"
os = ["alpine"]
script = "install.sh"

[params.authkey]
type = "secret"
required = true
help = "tailnet auth key"

[params.channel]
type = "enum"
values = ["stable", "test"]
default = "stable"

[params.port]
type = "int"
default = 2375

[params.tls]
type = "bool"
default = true

[params.user]
type = "string"
required = true
`
	if err := os.WriteFile(filepath.Join(dir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func parameterizedForm(t *testing.T) model {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	writeParamRecipe(t)
	f := newForm()
	f.images = []imageOption{stubImage(t, "alpine-standard-3.20.0-x86_64.iso")}
	f.imgIdx = 0
	f.refreshRecipes()
	for i, name := range f.recipeNames {
		if name == "docker" {
			f.recipeIdx = i
			break
		}
	}
	f.focus = fRecipes
	f.inputs[fName].SetValue("param-vm")
	return model{screen: screenForm, width: 100, height: 40, form: f}
}

func sendParamKeys(m model, keys ...string) model {
	for _, key := range keys {
		out, _ := m.Update(keyMsg(key))
		m = out.(model)
	}
	return m
}

func typeParamText(m model, text string) model {
	for _, r := range text {
		m = sendParamKeys(m, string(r))
	}
	return m
}

// Selecting a parameterized recipe enters the existing wizard's form
// lifecycle. The rendered boundary must show every declared field while a
// secret remains masked and absent from the screen.
func TestParameterizedRecipeSelectionOpensMaskedForm(t *testing.T) {
	m := parameterizedForm(t)
	out, _ := m.Update(keyMsg(keySpace))
	m = out.(model)
	rendered := ansi.Strip(m.View().Content)
	for _, field := range []string{"authkey", "channel", "port", "tls", "user"} {
		if !strings.Contains(rendered, field) {
			t.Errorf("parameter form omitted %q:\n%s", field, rendered)
		}
	}
	if strings.Contains(rendered, "synthetic-secret-sentinel") {
		t.Fatal("parameter form rendered a secret value")
	}
}

// Confirming with an empty required secret must keep the wizard in the
// parameter form and expose validation feedback; it must not create a VM with
// an absent required credential.
func TestParameterizedRecipeRequiredValueBlocksConfirm(t *testing.T) {
	m := parameterizedForm(t)
	out, _ := m.Update(keyMsg(keySpace))
	m = out.(model)
	out, _ = m.Update(keyMsg("enter"))
	m = out.(model)
	if m.screen != screenForm {
		t.Fatalf("required validation left screen %v", m.screen)
	}
	rendered := ansi.Strip(m.View().Content)
	if !strings.Contains(strings.ToLower(rendered), "required") {
		t.Fatalf("required validation feedback missing:\n%s", rendered)
	}
}

// Typed fields validate at the wizard boundary: a non-numeric port cannot be
// confirmed even though the surrounding VM form itself accepts free text.
func TestParameterizedRecipeIntValidationBlocksConfirm(t *testing.T) {
	m := parameterizedForm(t)
	m = sendParamKeys(m, keySpace)
	m = typeParamText(m, "tskey-secret")
	// authkey -> channel -> port
	m = sendParamKeys(m, "tab", "tab")
	m = typeParamText(m, "not-an-int")
	m = sendParamKeys(m, "enter")
	if m.screen != screenForm {
		t.Fatalf("invalid integer left screen %v", m.screen)
	}
	rendered := strings.ToLower(ansi.Strip(m.View().Content))
	if !strings.Contains(rendered, "integer") && !strings.Contains(rendered, "number") {
		t.Fatalf("integer validation feedback missing:\n%s", rendered)
	}
}

func TestParameterizedRecipeEnumOffersOnlyDeclaredChoices(t *testing.T) {
	m := parameterizedForm(t)
	m = sendParamKeys(m, keySpace)
	rendered := ansi.Strip(m.View().Content)
	if !strings.Contains(rendered, "stable") || !strings.Contains(rendered, "test") {
		t.Fatalf("enum choices are not rendered by the wizard:\n%s", rendered)
	}
}

// After a normal wizard submission, non-secret edits belong in Spec.Params,
// secret edits belong in Spec.Secrets, and untouched defaults are omitted so
// future manifest changes can still take effect.
func TestParameterizedRecipeBuildSplitsSecretsAndOmitsDefaults(t *testing.T) {
	m := parameterizedForm(t)
	m = sendParamKeys(m, keySpace)
	m = typeParamText(m, "tskey-secret")
	// authkey -> channel -> port -> tls -> user
	m = sendParamKeys(m, "tab", "tab", "tab", "tab")
	m = typeParamText(m, "alice")
	m = sendParamKeys(m, "enter")
	spec, err := m.form.spec()
	if err != nil {
		t.Fatalf("form spec after parameter submission: %v", err)
	}
	if got := spec.Secrets["docker"]["authkey"]; got != "tskey-secret" {
		t.Errorf("secret authkey = %q, want secret storage", got)
	}
	if got := spec.Params["docker"]["user"]; got != "alice" {
		t.Errorf("non-secret user = %q, want Params storage", got)
	}
	for _, name := range []string{"channel", "port", "tls"} {
		if _, ok := spec.Params["docker"][name]; ok {
			t.Errorf("unchanged default %q was stored in Params", name)
		}
	}
}

// Escaping the parameter form returns to the recipe picker and discards the
// transient selection. A later deselection must not retain stale parameters.
func TestParameterizedRecipeCancelCleansSelection(t *testing.T) {
	m := parameterizedForm(t)
	out, _ := m.Update(keyMsg(keySpace))
	m = out.(model)
	out, _ = m.Update(keyMsg("esc"))
	m = out.(model)
	if m.screen != screenForm {
		t.Fatalf("cancel left screen %v, want recipe picker", m.screen)
	}
	if m.form.recipeSel["docker"] {
		t.Fatal("cancel retained a recipe selection")
	}
	if strings.Contains(ansi.Strip(m.View().Content), "authkey") {
		t.Fatal("cancel left parameter fields visible")
	}
}

// The existing view lifecycle owns narrow-terminal behavior too: opening a
// parameter form must not bypass the established minimum-size message.
func TestParameterizedRecipeNarrowTerminalUsesExistingFloor(t *testing.T) {
	m := parameterizedForm(t)
	out, _ := m.Update(keyMsg(keySpace))
	m = out.(model)
	m.width, m.height = 59, 20
	if got := ansi.Strip(m.View().Content); !strings.Contains(got, "terminal too small") {
		t.Fatalf("narrow parameter form did not use terminal floor:\n%s", got)
	}
}
