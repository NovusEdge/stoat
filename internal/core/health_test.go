package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestVMHealthFolds(t *testing.T) {
	tests := []struct {
		name string
		in   []RecipeHealth
		want Health
	}{
		{"empty", nil, HealthUnknown},
		{"all unknown", []RecipeHealth{{Status: HealthUnknown}}, HealthUnknown},
		{"one ok", []RecipeHealth{{Status: HealthOK}, {Status: HealthUnknown}}, HealthOK},
		{"one failed", []RecipeHealth{{Status: HealthOK}, {Status: HealthFailed}}, HealthFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VMHealth(tt.in); got != tt.want {
				t.Errorf("VMHealth = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyFailsOnHealthCheckAndPersistsResult(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithOutput(t, dir)
	const secret = "health-output-secret"
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"docker"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveSecrets(v.Dir, config.Secrets{"docker": {"authkey": secret}}); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installHealthSSH(t, false, t.TempDir())

	err := Apply(context.Background(), v.Name, ApplyOpts{})
	if err == nil {
		t.Fatal("Apply succeeded with a failing health check")
	}
	if !strings.Contains(err.Error(), "docker: health check failed after 30s: cannot connect to the docker daemon") {
		t.Errorf("err = %v", err)
	}
	got, err := config.Load(v.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied["docker"].Health != string(HealthFailed) {
		t.Errorf("health = %q, want failed", got.Applied["docker"].Health)
	}
	if got.Applied["docker"].Outputs["captured"] != "<redacted>" {
		t.Errorf("captured output = %q, want redacted", got.Applied["docker"].Outputs["captured"])
	}
	vmToml, err := os.ReadFile(filepath.Join(got.Dir, "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(vmToml), secret) {
		t.Fatalf("health output secret leaked into vm.toml: %s", vmToml)
	}
	plan, err := PlanApply(v.Name, ApplyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Action != "skip" {
		t.Fatalf("plan after failed health = %+v, want skip", plan)
	}
}

func TestApplyWithoutHealthCheckRecordsUnknown(t *testing.T) {
	dir := root(t)
	writeHealthRecipe(t, dir, false)
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"docker"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	installHealthSSH(t, true)

	if err := Apply(context.Background(), v.Name, ApplyOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(v.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied["docker"].Health != string(HealthUnknown) {
		t.Errorf("health = %q, want unknown", got.Applied["docker"].Health)
	}
}

func TestHealthChecksPropagatesCancellation(t *testing.T) {
	dir := root(t)
	writeHealthRecipe(t, dir, true)
	v := &config.VM{Name: "work", Dir: filepath.Join(dir, "work"), OS: "alpine", Recipes: []string{"docker"}, Applied: map[string]config.AppliedRecipe{"docker": {}}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HealthChecks(ctx, v.Name)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HealthChecks error = %v, want context.Canceled", err)
	}
}

func TestRecipeHealthTimeoutKeepsRawDeclarationAcrossListAndShow(t *testing.T) {
	dir := root(t)
	writeHealthRecipeWithTimeoutNamed(t, dir, "health-sixty", "60s")
	writeHealthRecipeWithTimeoutNamed(t, dir, "health-default", "")

	shown, err := RecipeShow("health-sixty")
	if err != nil {
		t.Fatal(err)
	}
	if shown.Health == nil || shown.Health.Timeout != "60s" {
		t.Fatalf("RecipeShow health = %+v, want raw 60s timeout", shown.Health)
	}
	defaultShown, err := RecipeShow("health-default")
	if err != nil {
		t.Fatal(err)
	}
	if defaultShown.Health == nil || defaultShown.Health.Timeout != "30s" {
		t.Fatalf("RecipeShow omitted timeout = %+v, want 30s", defaultShown.Health)
	}

	listed, err := Recipes(RecipeFilter{OS: "alpine"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]RecipeHealthSpec{}
	for _, recipe := range listed {
		if recipe.Health != nil {
			seen[recipe.Name] = *recipe.Health
		}
	}
	if got := seen["health-sixty"].Timeout; got != "60s" {
		t.Errorf("Recipes health-sixty timeout = %q, want 60s", got)
	}
	if got := seen["health-default"].Timeout; got != "30s" {
		t.Errorf("Recipes health-default timeout = %q, want 30s", got)
	}
}

func writeHealthRecipe(t *testing.T, rootDir string, withHealth bool) {
	t.Helper()
	d := filepath.Join(rootDir, "recipes", "docker")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"docker\"\nscript = \"install.sh\"\n"
	if withHealth {
		manifest += "\n[health]\ncheck = \"docker info\"\n"
	}
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte("#!/bin/sh\necho provisioned\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeHealthRecipeWithOutput(t *testing.T, rootDir string) {
	t.Helper()
	d := filepath.Join(rootDir, "recipes", "docker")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"docker\"\nscript = \"install.sh\"\n\n[params.authkey]\ntype = \"secret\"\nrequired = true\n\n[health]\ncheck = \"docker info\"\n"
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf 'captured=%s\\n' \"$STOAT_PARAM_AUTHKEY\" > \"$STOAT_OUTPUT\"\n"
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func installHealthSSH(t *testing.T, succeedHealth bool, outputRoots ...string) {
	t.Helper()
	bin := t.TempDir()
	check := "echo 'cannot connect to the docker daemon' >&2\nexit 1"
	if succeedHealth {
		check = "exit 0"
	}
	script := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *'docker info'*)\n" + check + "\n;; esac\n"
	if len(outputRoots) > 0 {
		root := strings.ReplaceAll(outputRoots[0], "#", "\\#")
		script += "case \"$input\" in *'STOAT_RECIPE=docker'*) safe=$(printf '%s' \"$input\" | sed 's#/tmp/.stoat-out#" + root + "#g'); printf '%s' \"$safe\" | sh -s; exit $?;; esac\n"
		script += "case \"$*\" in *'cat /tmp/.stoat-out/docker'*) cat " + filepath.Join(outputRoots[0], "docker") + ";; esac\n"
	}
	script += "exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
