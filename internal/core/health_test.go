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
	writeHealthRecipe(t, dir, true)
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
	installHealthSSH(t, false)

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
	v := &config.VM{Name: "work", Dir: filepath.Join(dir, "work"), OS: "alpine"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := HealthChecks(ctx, v, []string{"docker"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HealthChecks error = %v, want context.Canceled", err)
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

func installHealthSSH(t *testing.T, succeedHealth bool) {
	t.Helper()
	bin := t.TempDir()
	check := "echo 'cannot connect to the docker daemon' >&2\nexit 1"
	if succeedHealth {
		check = "exit 0"
	}
	script := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *'docker info'*)\n" + check + "\n;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
