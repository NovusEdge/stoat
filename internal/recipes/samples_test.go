package recipes_test

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
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

func TestBundledCommonDeveloperRecipeContracts(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	wantOS := []string{"alpine", "arch", "debian", "fedora", "ubuntu"}
	checks := []struct {
		name    string
		outputs []string
	}{
		{name: "devtools", outputs: []string{"compiler", "editor", "git"}},
		{name: "python-dev", outputs: []string{"pip", "python", "python_version", "venv", "venv_python"}},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			m, ok, err := recipes.ManifestFor(tc.name)
			if err != nil || !ok {
				t.Fatalf("ManifestFor(%q) = ok %v, err %v", tc.name, ok, err)
			}
			if m.Schema != 3 || m.Run != "once" || m.Auto {
				t.Errorf("manifest contract = schema %d, run %q, auto %v; want schema 3, once, false", m.Schema, m.Run, m.Auto)
			}
			gotOS := append([]string(nil), m.OS...)
			slices.Sort(gotOS)
			if !slices.Equal(gotOS, wantOS) {
				t.Errorf("OS = %v, want set %v", m.OS, wantOS)
			}
			if len(m.Health.Check) == 0 {
				t.Error("health.check is empty")
			}
			outputNames := make([]string, 0, len(m.Outputs))
			for name := range m.Outputs {
				outputNames = append(outputNames, name)
			}
			slices.Sort(outputNames)
			if !slices.Equal(outputNames, tc.outputs) {
				t.Errorf("outputs = %v, want %v", outputNames, tc.outputs)
			}
			for _, osName := range wantOS {
				if m.Scripts[osName] == "" {
					t.Errorf("scripts has no explicit %q mapping", osName)
					continue
				}
				if _, err := m.ScriptContent(osName); err != nil {
					t.Errorf("ScriptContent(%q): %v", osName, err)
				}
			}

			if tc.name != "python-dev" {
				return
			}
			userParam, ok := m.Params["user"]
			if !ok || userParam.Type != "string" || !userParam.Required || userParam.Default != "" {
				t.Errorf("user param = %+v, want required string with no default", userParam)
			}
			venvParam, ok := m.Params["venv_dir"]
			if !ok || venvParam.Type != "string" || venvParam.Required || venvParam.Default != "" {
				t.Errorf("venv_dir param = %+v, want optional string defaulting empty", venvParam)
			}
		})
	}
}

func TestBundledPythonDevCreatesAndPreservesVenv(t *testing.T) {
	body := bundledScript(t, "python-dev", "alpine")
	account := currentTestAccount(t)
	root := t.TempDir()
	venvDir := filepath.Join(root, "existing-env")
	outputPath := filepath.Join(root, "output")
	callsPath := filepath.Join(root, "package-calls")

	if output, err := exec.Command("python3", "-m", "venv", venvDir).CombinedOutput(); err != nil {
		t.Fatalf("create hermetic venv: %v\n%s", err, output)
	}
	sentinel := filepath.Join(venvDir, "stoat-sentinel")
	if err := os.WriteFile(sentinel, []byte("keep-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		if output, err := runBundledPython(t, body, account.Username, venvDir, outputPath, callsPath); err != nil {
			t.Fatalf("run %d: %v\n%s", run+1, err, output)
		}
		got, err := os.ReadFile(sentinel)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "keep-me\n" {
			t.Fatalf("run %d sentinel = %q, want it preserved", run+1, got)
		}
		assertPythonIsolation(t, filepath.Join(venvDir, "bin", "python"))
		if got := fileUID(t, venvDir); got != account.Uid {
			t.Errorf("run %d environment uid = %q, want configured account uid %q", run+1, got, account.Uid)
		}
	}

	createdDir := filepath.Join(root, "created-env")
	if output, err := runBundledPython(t, body, account.Username, createdDir, filepath.Join(root, "created-output"), callsPath); err != nil {
		t.Fatalf("create missing environment: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(createdDir, "pyvenv.cfg")); err != nil {
		t.Fatalf("created environment missing pyvenv.cfg: %v", err)
	}
	if got := fileUID(t, createdDir); got != account.Uid {
		t.Errorf("created environment uid = %q, want configured account uid %q", got, account.Uid)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	installRequests := 0
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if !strings.HasPrefix(line, "install ") {
			continue
		}
		installRequests++
		if line != "install python3 py3-pip" {
			t.Errorf("Alpine package request = %q, want exactly python3 py3-pip", strings.TrimPrefix(line, "install "))
		}
	}
	if installRequests == 0 {
		t.Error("Alpine recipe made no captured package installation request")
	}
}

// Alpine ships a sudoers file that grants root nothing, so sudo is on PATH and
// still refuses every command. The recipe must fall through to su instead of
// failing the runcmd and leaving cloud-init in error.
func TestBundledPythonDevFallsBackWhenEscalationRefuses(t *testing.T) {
	t.Setenv("STOAT_FAKE_NO_ESCALATION", "1")
	account := currentTestAccount(t)
	root := t.TempDir()

	for _, guestOS := range []string{"alpine", "debian", "arch", "fedora"} {
		body := bundledScript(t, "python-dev", guestOS)
		venvDir := filepath.Join(root, guestOS, "env")
		output, err := runBundledPython(t, body, account.Username, venvDir,
			filepath.Join(root, guestOS+"-output"), filepath.Join(root, guestOS+"-calls"))
		if err != nil {
			t.Fatalf("%s: %v\n%s", guestOS, err, output)
		}
		if _, err := os.Stat(filepath.Join(venvDir, "pyvenv.cfg")); err != nil {
			t.Errorf("%s: environment was not created: %v", guestOS, err)
		}
	}
}

func TestBundledPythonDevRefusesInvalidTargets(t *testing.T) {
	body := bundledScript(t, "python-dev", "alpine")
	account := currentTestAccount(t)
	root := t.TempDir()

	t.Run("missing user", func(t *testing.T) {
		venvDir := filepath.Join(root, "missing-user-env")
		output, err := runBundledPython(t, body, "stoat-user-that-does-not-exist", venvDir, filepath.Join(root, "missing-user-output"), filepath.Join(root, "missing-user-calls"))
		if err == nil {
			t.Fatalf("script succeeded, want missing-user error; output=%s", output)
		}
		want := `python-dev: user "stoat-user-that-does-not-exist": user does not exist`
		if !strings.Contains(string(output), want) {
			t.Errorf("error = %q, want exact actionable message %q", output, want)
		}
		if _, err := os.Stat(venvDir); !os.IsNotExist(err) {
			t.Errorf("missing-user target exists after refusal: %v", err)
		}
	})

	t.Run("occupied directory", func(t *testing.T) {
		venvDir := filepath.Join(root, "occupied")
		if err := os.MkdirAll(venvDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(venvDir, "user-data")
		before := []byte("do not replace\n")
		if err := os.WriteFile(sentinel, before, 0o644); err != nil {
			t.Fatal(err)
		}
		output, err := runBundledPython(t, body, account.Username, venvDir, filepath.Join(root, "occupied-output"), filepath.Join(root, "occupied-calls"))
		if err == nil {
			t.Fatalf("script succeeded, want occupied-directory error; output=%s", output)
		}
		want := fmt.Sprintf("python-dev: venv_dir %q: existing path is not a Python virtual environment", venvDir)
		if !strings.Contains(string(output), want) {
			t.Errorf("error = %q, want exact actionable message %q", output, want)
		}
		after, readErr := os.ReadFile(sentinel)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Errorf("occupied directory data changed from %q to %q", before, after)
		}
	})

	t.Run("pip-less environment", func(t *testing.T) {
		venvDir := filepath.Join(root, "pip-less")
		if output, err := exec.Command("python3", "-m", "venv", "--without-pip", venvDir).CombinedOutput(); err != nil {
			t.Fatalf("create pip-less venv: %v\n%s", err, output)
		}
		sentinel := filepath.Join(venvDir, "user-data")
		beforeSentinel := []byte("do not replace\n")
		if err := os.WriteFile(sentinel, beforeSentinel, 0o644); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(venvDir, "pyvenv.cfg")
		beforeConfig, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}

		output, err := runBundledPython(t, body, account.Username, venvDir, filepath.Join(root, "pip-less-output"), filepath.Join(root, "pip-less-calls"))
		if err == nil {
			t.Fatalf("script succeeded, want incompatible-environment error; output=%s", output)
		}
		want := fmt.Sprintf("python-dev: venv_dir %q: existing path is not a compatible Python virtual environment", venvDir)
		if !strings.Contains(string(output), want) {
			t.Errorf("error = %q, want exact actionable message %q", output, want)
		}
		afterSentinel, readErr := os.ReadFile(sentinel)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(afterSentinel) != string(beforeSentinel) {
			t.Errorf("pip-less sentinel changed from %q to %q", beforeSentinel, afterSentinel)
		}
		afterConfig, readErr := os.ReadFile(configPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(afterConfig) != string(beforeConfig) {
			t.Errorf("pip-less pyvenv.cfg changed from %q to %q", beforeConfig, afterConfig)
		}
	})
}

func TestBundledPythonDevOutputsAndQuotesParameters(t *testing.T) {
	body := bundledScript(t, "python-dev", "alpine")
	account := currentTestAccount(t)
	root := t.TempDir()
	marker := filepath.Join(root, "injected")
	venvDir := filepath.Join(root, `venv dir;$(touch "$STOAT_MARKER")`)
	outputPath := filepath.Join(root, "output")
	if output, err := runBundledPythonWithMarker(t, body, account.Username, venvDir, outputPath, filepath.Join(root, "calls"), marker); err != nil {
		t.Fatalf("literal path run: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(venvDir, "pyvenv.cfg")); err != nil {
		t.Fatalf("literal path was not created: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("parameter triggered command substitution; marker stat error = %v", err)
	}
	outputs := parseOutputs(t, outputPath)
	if outputs["venv"] != venvDir || outputs["venv_python"] != filepath.Join(venvDir, "bin", "python") {
		t.Errorf("environment outputs = %#v, want venv=%q and venv_python=%q", outputs, venvDir, filepath.Join(venvDir, "bin", "python"))
	}
	for _, name := range []string{"python", "python_version", "pip"} {
		if outputs[name] == "" {
			t.Errorf("output %q is empty; outputs = %#v", name, outputs)
		}
	}

	smokeOutput := filepath.Join(root, "smoke-output")
	if output, err := runBundledPython(t, body, account.Username, "", smokeOutput, filepath.Join(root, "smoke-calls")); err != nil {
		t.Fatalf("smoke-only run: %v\n%s", err, output)
	}
	smoke := parseOutputs(t, smokeOutput)
	if smoke["venv"] != "" || smoke["venv_python"] != "" {
		t.Errorf("smoke outputs = %#v, want empty venv and venv_python", smoke)
	}
}

func bundledScript(t *testing.T, name, osName string) string {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	m, ok, err := recipes.ManifestFor(name)
	if err != nil || !ok {
		t.Fatalf("ManifestFor(%q) = ok %v, err %v", name, ok, err)
	}
	body, err := m.ScriptContent(osName)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func currentTestAccount(t *testing.T) *user.User {
	t.Helper()
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if account.Username == "" || account.Uid == "" {
		t.Fatalf("current account is missing username or uid: %+v", account)
	}
	return account
}

func runBundledPython(t *testing.T, body, account, venvDir, outputPath, callsPath string) ([]byte, error) {
	t.Helper()
	return runBundledPythonWithMarker(t, body, account, venvDir, outputPath, callsPath, "")
}

func runBundledPythonWithMarker(t *testing.T, body, account, venvDir, outputPath, callsPath, marker string) ([]byte, error) {
	t.Helper()
	prefix := `stoat_pkg_setup() { printf 'setup\n' >> "$STOAT_PKG_CALLS"; }
stoat_pkg_install() { printf 'install %s\n' "$*" >> "$STOAT_PKG_CALLS"; }
`
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHermeticCommandFakes(t, bin)
	env := append(os.Environ(),
		"HOME="+t.TempDir(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STOAT_PARAM_USER="+account,
		"STOAT_PARAM_VENV_DIR="+venvDir,
		"STOAT_OUTPUT="+outputPath,
		"STOAT_PKG_CALLS="+callsPath,
		"STOAT_FAKE_PACKAGE_CALLS="+callsPath+".package-manager",
	)
	if marker != "" {
		env = append(env, "STOAT_MARKER="+marker)
	}
	cmd := exec.Command("sh", "-eu", "-c", prefix+body)
	cmd.Env = env
	return cmd.CombinedOutput()
}

func writeHermeticCommandFakes(t *testing.T, bin string) {
	t.Helper()
	accountSwitch := `#!/bin/sh
set -eu
if [ -n "${STOAT_FAKE_NO_ESCALATION:-}" ]; then
  echo "root is not in the sudoers file." >&2
  exit 1
fi
while [ "$#" -gt 0 ]; do
  case "$1" in
    -u|-g|-s) shift 2;;
    --) shift; break;;
    -*) shift;;
    *) break;;
  esac
done
exec "$@"
`
	for _, name := range []string{"runuser", "sudo"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(accountSwitch), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// busybox su, which is what Alpine has, recognises options anywhere on the
	// command line. A -p among the trailing arguments becomes su's own
	// preserve-environment flag and never reaches the command, so this fake
	// drops every dash argument and passes nothing positional to the shell.
	if err := os.WriteFile(filepath.Join(bin, "su"), []byte(`#!/bin/sh
set -eu
user=
command=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -c) command=$2; shift 2;;
    -s) shift 2;;
    -*) shift;;
    *) [ -n "$user" ] || user=$1; shift;;
  esac
done
[ -n "$command" ]
exec sh -c "$command"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	packageManager := `#!/bin/sh
set -eu
printf '%s %s\n' "$0" "$*" >> "$STOAT_FAKE_PACKAGE_CALLS"
exit 0
`
	for _, name := range []string{"apk", "apt-get", "dnf", "pacman", "zypper"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(packageManager), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func assertPythonIsolation(t *testing.T, pythonPath string) {
	t.Helper()
	probe := `import sys; assert sys.prefix != sys.base_prefix; print(sys.prefix)`
	if output, err := exec.Command(pythonPath, "-c", probe).CombinedOutput(); err != nil {
		t.Fatalf("isolated interpreter probe: %v\n%s", err, output)
	}
	if output, err := exec.Command(pythonPath, "-m", "pip", "--version").CombinedOutput(); err != nil {
		t.Fatalf("isolated pip probe: %v\n%s", err, output)
	}
	systemOutput, err := exec.Command("python3", "-c", "import sys; print(sys.prefix)").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	venvOutput, err := exec.Command(pythonPath, "-c", "import sys; print(sys.prefix)").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(systemOutput)) == strings.TrimSpace(string(venvOutput)) {
		t.Errorf("venv prefix = %q, same as system prefix", strings.TrimSpace(string(venvOutput)))
	}
}

func fileUID(t *testing.T, path string) string {
	t.Helper()
	output, err := exec.Command("stat", "-c", "%u", path).CombinedOutput()
	if err != nil {
		t.Fatalf("stat %s: %v\n%s", path, err, output)
	}
	return strings.TrimSpace(string(output))
}

func parseOutputs(t *testing.T, path string) map[string]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Errorf("malformed output line %q", line)
			continue
		}
		out[name] = value
	}
	return out
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
			if !strings.Contains(string(calls), "gpg-existing=true") {
				t.Fatalf("gpg did not exercise an existing-keyring overwrite: %q", calls)
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
batch=false
yes=false
no_tty=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --batch) batch=true;;
    --yes) yes=true;;
    --no-tty) no_tty=true;;
    -o) out=$2; shift;;
  esac
  shift
done
target=$FAKE_ROOT$out
existing=false
if [ -e "$target" ]; then existing=true; fi
printf 'gpg-existing=%s\n' "$existing" >> "$FAKE_ROOT/calls"
if [ "$batch" != true ] || [ "$yes" != true ] || [ "$no_tty" != true ]; then
  printf 'gpg missing noninteractive overwrite flags\n' >&2
  exit 43
fi
mkdir -p "$(dirname "$target")"
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
