package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParamsRoundTrip(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	v.SetParam("docker", "user", "dev")
	v.SetParam("docker", "channel", "stable")
	v.Recipes = []string{"docker"}
	v.Applied = map[string]AppliedRecipe{
		"docker": {
			Version: "1.2.0", Hash: "sha256:abc", At: time.Unix(0, 0).UTC(),
			Outputs: map[string]string{"socket": "/var/run/docker.sock"},
			Health:  "ok",
		},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(Root(), "work", "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, `recipes = ["docker"]`) {
		t.Errorf("recipes array was not preserved:\n%s", text)
	}
	if strings.Contains(text, "[recipes.docker]") {
		t.Errorf("params were written under the recipes array:\n%s", text)
	}
	if !strings.Contains(text, "[applied.docker.outputs]") {
		t.Errorf("outputs were not written as an applied subtable:\n%s", text)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[params") && !strings.HasPrefix(trimmed, "[applied") {
			continue
		}
		j := i - 1
		for j >= 0 && strings.TrimSpace(lines[j]) == "" {
			j--
		}
		if j < 0 || !strings.Contains(lines[j], "written by stoat; do not edit") {
			t.Errorf("table %q lacks the stoat-owned comment", trimmed)
		}
	}
	got, err := Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := got.Param("docker", "user"); !ok || u != "dev" {
		t.Errorf("user = %q %v, want dev true", u, ok)
	}
	if got.Applied["docker"].Outputs["socket"] != "/var/run/docker.sock" {
		t.Errorf("outputs = %v", got.Applied["docker"].Outputs)
	}
	if got.Applied["docker"].Health != "ok" {
		t.Errorf("health = %q, want ok", got.Applied["docker"].Health)
	}
}

func TestParamAccessorsTrackEmptyValues(t *testing.T) {
	v := &VM{}
	v.SetParam("docker", "user", "")
	if got, ok := v.Param("docker", "user"); !ok || got != "" {
		t.Errorf("empty value = %q %v, want empty true", got, ok)
	}
	v.UnsetParam("docker", "user")
	if _, ok := v.Param("docker", "user"); ok {
		t.Error("unset parameter remains present")
	}
	if _, ok := v.Params["docker"]; ok {
		t.Error("empty recipe table remains after unsetting its last value")
	}
}

func TestUnsetParamDropsTheTableWhenEmpty(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	v.SetParam("docker", "user", "dev")
	v.UnsetParam("docker", "user")
	if _, ok := v.Params["docker"]; ok {
		t.Error("an empty recipe table stays in vm.toml")
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(Root(), "work", "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "[params.docker]") {
		t.Errorf("empty table written:\n%s", b)
	}
}
