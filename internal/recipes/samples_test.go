package recipes_test

import (
	"os"
	"os/exec"
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

// The canonical VM sample documents both stoat-owned applied tables. The
// nested outputs table is independently rewritten, so it needs the same
// ownership marker as its parent.
func TestVMSampleAppliedOutputsHasOwnershipComment(t *testing.T) {
	body, err := os.ReadFile("../../docs/reference/samples/vm.toml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(body), "\n")
	for _, target := range []string{"[applied.docker]", "[applied.docker.outputs]"} {
		found := false
		for i, line := range lines {
			if strings.TrimSpace(line) != target {
				continue
			}
			found = true
			j := i - 1
			for j >= 0 && strings.TrimSpace(lines[j]) == "" {
				j--
			}
			if j < 0 || !strings.Contains(strings.ToLower(lines[j]), "written by stoat; do not edit") {
				t.Errorf("sample table %q lacks an ownership comment", target)
			}
			break
		}
		if !found {
			t.Errorf("sample missing table %q", target)
		}
	}
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

// The Debian override and the default Docker script are real shell programs,
// so run each twice against command fakes. The fakes redirect every intended
// write below a temporary root and make curl non-networking; the second run
// proves the keyring is deliberately overwritten rather than prompting.
func TestBundledDockerScriptsRerunWithNonInteractiveKeyring(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	m, ok, err := recipes.ManifestFor("docker")
	if err != nil || !ok {
		t.Fatalf("ManifestFor(docker) = ok %v, err %v", ok, err)
	}
	for _, tc := range []struct {
		name string
		os   string
	}{
		{name: "debian-override", os: "debian"},
		{name: "default", os: "unknown-os"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := m.ScriptContent(tc.os)
			if err != nil {
				t.Fatal(err)
			}
			fakeRoot := t.TempDir()
			bin := filepath.Join(fakeRoot, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			writeBundledFakeCommands(t, bin)
			env := append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_ROOT="+fakeRoot,
				"STOAT_PARAM_USER=dev",
				"STOAT_OUTPUT="+filepath.Join(fakeRoot, "output"),
			)
			for run := 0; run < 2; run++ {
				cmd := exec.Command("sh", "-eu", "-c", body)
				cmd.Env = env
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s run %d failed: %v\n%s", tc.name, run+1, err, output)
				}
				keyring := filepath.Join(fakeRoot, "etc", "apt", "keyrings", "docker.gpg")
				got, err := os.ReadFile(keyring)
				if err != nil {
					t.Fatalf("run %d keyring missing: %v", run+1, err)
				}
				if string(got) != "fake-key\n" {
					t.Fatalf("run %d keyring = %q, want overwritten fake key", run+1, got)
				}
			}
			calls, err := os.ReadFile(filepath.Join(fakeRoot, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			curlCalls := 0
			for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
				if strings.HasPrefix(line, "curl ") {
					curlCalls++
				}
			}
			if curlCalls != 2 {
				t.Fatalf("curl calls = %q, want one per run", calls)
			}
			if output, err := os.ReadFile(filepath.Join(fakeRoot, "output")); err != nil || strings.Count(string(output), "socket=/var/run/docker.sock\n") != 2 {
				t.Fatalf("STOAT_OUTPUT = %q, err %v, want one output per run", output, err)
			}
		})
	}
}

func writeBundledFakeCommands(t *testing.T, bin string) {
	t.Helper()
	commands := map[string]string{
		"apt-get": `#!/bin/sh
printf 'apt-get %s\n' "$*" >> "$FAKE_ROOT/calls"
`,
		"curl": `#!/bin/sh
printf 'curl %s\n' "$*" >> "$FAKE_ROOT/calls"
printf 'fake-key\n'
`,
		"dpkg": `#!/bin/sh
printf 'amd64\n'
`,
		"docker": `#!/bin/sh
if [ "${1:-}" = info ]; then exit 0; fi
if [ "${1:-}" = version ]; then printf '24.0.0\n'; fi
`,
		"gpg": `#!/bin/sh
out=
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then out=$2; shift 2; continue; fi
  shift
done
mkdir -p "$FAKE_ROOT$(dirname "$out")"
cat > "$FAKE_ROOT$out"
`,
		"id": `#!/bin/sh
exit 1
`,
		"install": `#!/bin/sh
dir=false
out=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -d) dir=true; shift;;
    -m) shift 2;;
    -*) shift;;
    *) out=$1; shift;;
  esac
done
target=$FAKE_ROOT$out
if $dir; then mkdir -p "$target"; else mkdir -p "$(dirname "$target")"; : > "$target"; fi
`,
		"systemctl": `#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$FAKE_ROOT/calls"
`,
		"tee": `#!/bin/sh
target=$1
mkdir -p "$FAKE_ROOT$(dirname "$target")"
cat > "$FAKE_ROOT$target"
`,
		"useradd": `#!/bin/sh
printf 'useradd %s\n' "$*" >> "$FAKE_ROOT/calls"
`,
		"usermod": `#!/bin/sh
printf 'usermod %s\n' "$*" >> "$FAKE_ROOT/calls"
`,
		"chmod": `#!/bin/sh
exit 0
`,
		"sleep": `#!/bin/sh
exit 0
`,
	}
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
