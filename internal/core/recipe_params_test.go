package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

const paramRecipeManifest = `schema = 3
name = "docker"
script = "install.sh"

[params.user]
type = "string"
default = "dev"

[params.port]
type = "int"
default = 2375

[params.authkey]
type = "secret"
required = true
`

func writeParamRecipe(t *testing.T, rootDir string) {
	t.Helper()
	d := filepath.Join(rootDir, "recipes", "docker")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(paramRecipeManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte("#!/bin/sh\necho provisioned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPlanApplyReportsParamsChangedAndIgnoresSecretValueChanges(t *testing.T) {
	dir := root(t)
	writeParamRecipe(t, dir)

	scriptHash, err := recipes.ScriptHash("docker", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]string{"user": "dev", "port": "2375"}
	combined, err := recipes.RecipeHash("docker", "alpine", params, []string{"authkey"})
	if err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: 2200,
		Recipes: []string{"docker"}, Params: map[string]map[string]string{"docker": params},
		Applied: map[string]config.AppliedRecipe{"docker": {
			Version: "1.0", Hash: combined, ScriptHash: scriptHash,
		}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSecrets(v.Dir, config.Secrets{"docker": {"authkey": "first-secret"}}); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanApply(v.Name, ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != "skip" {
		t.Fatalf("initial plan = %+v, want skip", plan)
	}

	if err := config.SaveSecrets(v.Dir, config.Secrets{"docker": {"authkey": "changed-secret"}}); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanApply(v.Name, ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != "skip" {
		t.Fatalf("secret-only plan = %+v, want skip", plan)
	}

	v.SetParam("docker", "user", "bob")
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanApply(v.Name, ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != "run" || plan[0].Reason != "params changed" {
		t.Fatalf("non-secret change plan = %+v, want run/params changed", plan)
	}
}

func TestCreatePersistsParamsAndSecretsUnderTheVMDirectory(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	writeParamRecipe(t, dir)

	_, err := Create(Spec{
		Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Recipes: []string{"docker"},
		Params:  map[string]map[string]string{"docker": {"user": "alice"}},
		Secrets: config.Secrets{"docker": {"authkey": "secret-value"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := v.Param("docker", "user"); !ok || got != "alice" {
		t.Errorf("stored user = %q/%v, want alice/true", got, ok)
	}
	contents, err := os.ReadFile(filepath.Join(v.Dir, "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "secret-value") {
		t.Fatal("vm.toml contains the secret value")
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets["docker"]["authkey"]; got != "secret-value" {
		t.Errorf("stored secret = %q, want secret-value", got)
	}
	info, err := os.Stat(v.SecretsPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("secrets mode = %#o, want 0600", got)
	}
}

func TestUpdateValidatesAndUnsetsSecretAndNonSecretParams(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	writeParamRecipe(t, dir)
	if _, err := Create(Spec{
		Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Recipes: []string{"docker"},
		Params:  map[string]map[string]string{"docker": {"user": "alice", "port": "2375"}},
		Secrets: config.Secrets{"docker": {"authkey": "old-secret"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Update("work", Patch{
		SetParams:   map[string]map[string]string{"docker": {"user": "bob"}},
		UnsetParams: map[string][]string{"docker": {"port", "authkey"}},
	}); err != nil {
		t.Fatal(err)
	}
	v, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := v.Param("docker", "user"); !ok || got != "bob" {
		t.Errorf("user = %q/%v, want bob/true", got, ok)
	}
	if _, ok := v.Param("docker", "port"); ok {
		t.Error("unset non-secret port remained in vm.toml")
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets["docker"]["authkey"]; ok {
		t.Error("unset secret authkey remained in secrets.toml")
	}

	if _, err := Update("work", Patch{UnsetParams: map[string][]string{"docker": {"missing"}}}); err == nil {
		t.Fatal("unknown parameter unset was accepted")
	}
	v, err = config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := v.Param("docker", "user"); !ok || got != "bob" {
		t.Errorf("failed update changed user = %q/%v", got, ok)
	}
}
