package recipes_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/tomlx"
)

// The samples are the format documentation. Decoding each one in Reject mode
// against its real struct means a field renamed in Go breaks this test rather
// than quietly leaving the docs wrong.
func TestSamplesDecodeInRejectMode(t *testing.T) {
	root := "../../docs/reference/samples"
	t.Run("recipe.toml", func(t *testing.T) {
		var m recipes.Manifest
		if err := tomlx.Decode(filepath.Join(root, "recipe.toml"), &m, tomlx.Reject); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("vm.toml", func(t *testing.T) {
		var v config.VM
		if err := tomlx.Decode(filepath.Join(root, "vm.toml"), &v, tomlx.Reject); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("guest.toml", func(t *testing.T) {
		var o guest.OS
		if err := tomlx.Decode(filepath.Join(root, "guest.toml"), &o, tomlx.Reject); err != nil {
			t.Fatal(err)
		}
	})
}

// The sample must also survive the manifest's own validation, not only the
// decoder: a sample that documents an illegal param teaches the wrong thing.
func TestSampleRecipeParses(t *testing.T) {
	m, err := recipes.ParseManifest("../../docs/reference/samples/recipe.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Params) != 5 || len(m.Outputs) != 1 || m.Health.Check == "" {
		t.Errorf("sample lost coverage: %+v", m)
	}
}

// recipe new must create every script path declared by the canonical sample;
// a manifest that names an OS override without its file is not a usable
// scaffold.
func TestNewScaffoldsEveryDeclaredScriptPath(t *testing.T) {
	canonical := filepath.Join(t.TempDir(), "recipe.toml")
	if err := os.WriteFile(canonical, []byte(recipes.SampleManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := recipes.ParseManifest(canonical)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("STOAT_HOME", t.TempDir())
	dir, err := recipes.New("mine", "alpine", "")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{m.Script: true}
	for _, script := range m.Scripts {
		paths[script] = true
	}
	for script := range paths {
		if _, err := os.Stat(filepath.Join(dir, script)); err != nil {
			t.Errorf("declared script %q was not scaffolded: %v", script, err)
		}
	}
}

func TestBundledTailscaleAuthkeyIsRequiredSecret(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	m, ok, err := recipes.ManifestFor("tailscale")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("bundled tailscale manifest missing")
	}
	authkey, ok := m.Params["authkey"]
	if !ok {
		t.Fatal("tailscale manifest has no authkey parameter")
	}
	if authkey.Type != "secret" || !authkey.Required || authkey.Default != "" {
		t.Fatalf("tailscale authkey = %+v, want required secret with no default", authkey)
	}
}

func TestBundledRecipeScriptsUseChangedParameterVerbs(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want []string
	}{
		{name: "docker", want: []string{"STOAT_PARAM_USER", "STOAT_OUTPUT", "socket=/var/run/docker.sock"}},
		{name: "tailscale", want: []string{"STOAT_PARAM_AUTHKEY", "tailscale up --authkey"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, ok, err := recipes.ManifestFor(tc.name)
			if err != nil || !ok {
				t.Fatalf("ManifestFor(%q) = ok %v, err %v", tc.name, ok, err)
			}
			body, err := m.ScriptContent("alpine")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("%s script missing %q", tc.name, want)
				}
			}
		})
	}
}
