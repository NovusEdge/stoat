package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func v3ParamsFixture(t *testing.T) Manifest {
	t.Helper()
	m, err := ParseManifest(writeManifestFile(t, t.TempDir(), v3Manifest))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolveFillsDefaults(t *testing.T) {
	m := v3ParamsFixture(t)
	got, err := Resolve(m, map[string]string{"user": "alice"}, map[string]string{"authkey": "tskey-abc"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"user": "alice", "port": "2375", "tls": "true",
		"channel": "stable", "authkey": "tskey-abc",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestResolveErrors(t *testing.T) {
	m := v3ParamsFixture(t)
	tests := []struct {
		name    string
		set     map[string]string
		secrets map[string]string
		want    string
	}{
		{
			name: "unknown param", set: map[string]string{"usr": "dev"},
			secrets: map[string]string{"authkey": "k"},
			want:    `docker: no param "usr"; has authkey, channel, port, tls, user`,
		},
		{
			name: "enum mismatch", set: map[string]string{"channel": "beta"},
			secrets: map[string]string{"authkey": "k"},
			want:    `docker.channel: "beta" is not one of stable, test`,
		},
		{
			name: "int mismatch", set: map[string]string{"port": "http"},
			secrets: map[string]string{"authkey": "k"},
			want:    `docker.port: "http" is not an integer`,
		},
		{
			name: "bool mismatch", set: map[string]string{"tls": "yes"},
			secrets: map[string]string{"authkey": "k"},
			want:    `docker.tls: "yes" is not true or false`,
		},
		{
			name: "required secret unset", set: map[string]string{},
			want: `docker.authkey: required secret is unset; run stoat update --secret docker.authkey`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(m, tt.set, tt.secrets)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEnvIsSortedAndUpperCased(t *testing.T) {
	got := Env("docker", map[string]string{"user": "dev", "channel": "stable"})
	want := []string{"STOAT_RECIPE=docker", "STOAT_PARAM_CHANNEL=stable", "STOAT_PARAM_USER=dev"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecipeHashCoversParamsAndSecretNames(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	installParamFixtureRecipe(t, "docker", v3Manifest, "#!/bin/sh\nset -e\n")

	base, err := RecipeHash("docker", "alpine", map[string]string{"user": "dev"}, []string{"authkey"})
	if err != nil {
		t.Fatal(err)
	}
	changedParam, err := RecipeHash("docker", "alpine", map[string]string{"user": "bob"}, []string{"authkey"})
	if err != nil {
		t.Fatal(err)
	}
	if changedParam == base {
		t.Error("a changed param left the hash unchanged")
	}
	addedSecret, err := RecipeHash("docker", "alpine", map[string]string{"user": "dev"}, []string{"authkey", "extra"})
	if err != nil {
		t.Fatal(err)
	}
	if addedSecret == base {
		t.Error("a newly set secret left the hash unchanged")
	}
}

func TestRecipeHashMatchesScriptHashWithoutParams(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	installParamFixtureRecipe(t, "xfce", "name = \"xfce\"\nscript = \"install.sh\"\n", "#!/bin/sh\nset -e\n")

	want, err := ScriptHash("xfce", "alpine")
	if err != nil {
		t.Fatal(err)
	}
	got, err := RecipeHash("xfce", "alpine", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("RecipeHash = %s, ScriptHash = %s", got, want)
	}
}

func installParamFixtureRecipe(t *testing.T, name, manifest, script string) {
	t.Helper()
	d := filepath.Join(dir(), name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
