package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/novusedge/stoat/internal/config"
)

func writeParamRecipe(t *testing.T) {
	t.Helper()
	baseDir := filepath.Join(config.Root(), "recipes", "param-base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baseManifest := `schema = 2
name = "param-base"
description = "parameter dependency"
os = ["alpine"]
script = "install.sh"
`
	if err := os.WriteFile(filepath.Join(baseDir, "recipe.toml"), []byte(baseManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(config.Root(), "recipes", "param-docker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema = 3
name = "param-docker"
description = "parameterized recipe"
os = ["alpine"]
script = "install.sh"
depends = ["param-base"]

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
		if name == "param-docker" {
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
	for _, defaultValue := range []string{"stable", "2375", "true"} {
		if !strings.Contains(rendered, defaultValue) {
			t.Errorf("parameter form omitted manifest default %q:\n%s", defaultValue, rendered)
		}
	}
	if strings.Contains(rendered, "dev") {
		t.Errorf("parameter form invented a user default not declared by the manifest:\n%s", rendered)
	}
	const sentinel = "synthetic-secret-sentinel"
	m = typeParamText(m, sentinel)
	spec, err := m.form.spec()
	if err != nil {
		t.Fatalf("form spec after typing secret: %v", err)
	}
	if got := spec.Secrets["param-docker"]["authkey"]; got != sentinel {
		t.Fatalf("wizard did not carry typed secret through spec: got %q, want %q", got, sentinel)
	}
	rendered = ansi.Strip(m.View().Content)
	if strings.Contains(rendered, sentinel) {
		t.Fatal("parameter form rendered the typed secret value")
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
	if got := spec.Secrets["param-docker"]["authkey"]; got != "tskey-secret" {
		t.Errorf("secret authkey = %q, want secret storage", got)
	}
	if got := spec.Params["param-docker"]["user"]; got != "alice" {
		t.Errorf("non-secret user = %q, want Params storage", got)
	}
	for _, name := range []string{"channel", "port", "tls"} {
		if _, ok := spec.Params["param-docker"][name]; ok {
			t.Errorf("unchanged default %q was stored in Params", name)
		}
	}
}

// A completed parameter form returns to the recipe picker with its values
// attached to the selected recipe. Deselecting that recipe must remove both
// its parameter values and dependencies that are no longer needed.
func TestParameterizedRecipeSubmitAndDeselectCleansSelection(t *testing.T) {
	m := parameterizedForm(t)
	m = sendParamKeys(m, keySpace)
	m = typeParamText(m, "submitted-secret")
	// authkey -> channel -> port -> tls -> user
	m = sendParamKeys(m, "tab", "tab", "tab", "tab")
	m = typeParamText(m, "submitted-user")
	m = sendParamKeys(m, "enter")

	spec, err := m.form.spec()
	if err != nil {
		t.Fatalf("form spec after parameter submission: %v", err)
	}
	if !containsString(spec.Recipes, "param-docker") || !containsString(spec.Recipes, "param-base") {
		t.Fatalf("submitted spec = %+v, want recipe and dependency selected", spec.Recipes)
	}
	if got := spec.Secrets["param-docker"]["authkey"]; got != "submitted-secret" {
		t.Fatalf("submitted secret = %q, want typed value", got)
	}
	if got := spec.Params["param-docker"]["user"]; got != "submitted-user" {
		t.Fatalf("submitted user = %q, want typed value", got)
	}

	m = sendParamKeys(m, keySpace)
	cleaned, err := m.form.spec()
	if err != nil {
		t.Fatalf("form spec after recipe deselection: %v", err)
	}
	if containsString(cleaned.Recipes, "param-docker") || containsString(cleaned.Recipes, "param-base") {
		t.Fatalf("deselected spec = %+v, retained recipe or dependency", cleaned.Recipes)
	}
	if _, ok := cleaned.Secrets["param-docker"]; ok {
		t.Errorf("deselected spec retained recipe secrets: %#v", cleaned.Secrets)
	}
	if _, ok := cleaned.Params["param-docker"]; ok {
		t.Errorf("deselected spec retained recipe params: %#v", cleaned.Params)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
	if m.form.recipeSel["param-docker"] {
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

func writeDependencyParamRecipes(t *testing.T) {
	t.Helper()
	baseDir := filepath.Join(config.Root(), "recipes", "dependency-base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baseManifest := `schema = 3
name = "dependency-base"
description = "required dependency parameters"
os = ["alpine"]
script = "install.sh"

[params.channel]
type = "enum"
values = ["stable", "canary"]
default = "stable"

[params.token]
type = "secret"
required = true

[params.owner]
type = "string"
required = true
`
	if err := os.WriteFile(filepath.Join(baseDir, "recipe.toml"), []byte(baseManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baseDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	parentDir := filepath.Join(config.Root(), "recipes", "dependency-parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentManifest := `schema = 2
name = "dependency-parent"
description = "parameterized dependency parent"
os = ["alpine"]
script = "install.sh"
depends = ["dependency-base"]
`
	if err := os.WriteFile(filepath.Join(parentDir, "recipe.toml"), []byte(parentManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func dependencyForm(t *testing.T) model {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	writeDependencyParamRecipes(t)
	f := newForm()
	f.images = []imageOption{stubImage(t, "alpine-standard-3.20.0-x86_64.iso")}
	f.imgIdx = 0
	f.refreshRecipes()
	for i, name := range f.recipeNames {
		if name == "dependency-parent" {
			f.recipeIdx = i
			break
		}
	}
	f.focus = fRecipes
	f.inputs[fName].SetValue("dependency-vm")
	return model{screen: screenForm, width: 100, height: 40, form: f}
}

// A selected parent must make a required secret and non-secret field from its
// dependency usable through the same wizard boundary. The enum is changed
// from its manifest default so the key path, not only the rendered options,
// is exercised; submitting then records values on the dependency and parent
// deselection removes both selections and their values.
func TestParameterizedDependencyFieldsSubmitAndDeselectThroughWizard(t *testing.T) {
	m := dependencyForm(t)
	m = sendParamKeys(m, keySpace)
	rendered := ansi.Strip(m.View().Content)
	for _, field := range []string{"channel", "token", "owner"} {
		if !strings.Contains(rendered, field) {
			t.Fatalf("dependency form omitted %q:\n%s", field, rendered)
		}
	}
	m = sendParamKeys(m, "down", "tab")
	m = typeParamText(m, "dependency-secret")
	m = sendParamKeys(m, "tab")
	m = typeParamText(m, "alice")
	m = sendParamKeys(m, "enter")

	spec, err := m.form.spec()
	if err != nil {
		t.Fatalf("dependency wizard spec = %v", err)
	}
	if !containsString(spec.Recipes, "dependency-parent") || !containsString(spec.Recipes, "dependency-base") {
		t.Fatalf("dependency recipes = %v, want parent and dependency", spec.Recipes)
	}
	if got := spec.Secrets["dependency-base"]["token"]; got != "dependency-secret" {
		t.Fatalf("dependency secret = %q, want typed value", got)
	}
	if got := spec.Params["dependency-base"]["owner"]; got != "alice" {
		t.Fatalf("dependency owner = %q, want typed value", got)
	}
	if got := spec.Params["dependency-base"]["channel"]; got != "canary" {
		t.Fatalf("dependency enum = %q, want non-default key selection", got)
	}

	m = sendParamKeys(m, keySpace)
	cleaned, err := m.form.spec()
	if err != nil {
		t.Fatalf("dependency spec after deselection = %v", err)
	}
	if containsString(cleaned.Recipes, "dependency-parent") || containsString(cleaned.Recipes, "dependency-base") {
		t.Fatalf("deselected dependency recipes = %v", cleaned.Recipes)
	}
	if _, ok := cleaned.Secrets["dependency-base"]; ok {
		t.Fatalf("deselected dependency retained secret: %#v", cleaned.Secrets)
	}
	if _, ok := cleaned.Params["dependency-base"]; ok {
		t.Fatalf("deselected dependency retained params: %#v", cleaned.Params)
	}
}
