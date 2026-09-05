package cloudinit

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// wrappedFragment is the shape WrapScripts' output parses into: enough to
// assert on write_files/runcmd without hand-parsing YAML in every test.
type wrappedFragment struct {
	WriteFiles []struct {
		Path        string `yaml:"path"`
		Permissions string `yaml:"permissions"`
		Content     string `yaml:"content"`
	} `yaml:"write_files"`
	Runcmd []string `yaml:"runcmd"`
}

func parseWrapped(t *testing.T, body string) wrappedFragment {
	t.Helper()
	if !strings.HasPrefix(body, "#cloud-config\n") {
		t.Fatalf("WrapScripts output does not start with #cloud-config:\n%s", body)
	}
	var f wrappedFragment
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(body, "#cloud-config\n")), &f); err != nil {
		t.Fatalf("WrapScripts output is not valid YAML: %v\n%s", err, body)
	}
	return f
}

func TestWrapScriptsEmpty(t *testing.T) {
	if got := WrapScripts(nil, ""); got != "" {
		t.Errorf("WrapScripts(nil) = %q, want empty string", got)
	}
	if got := WrapScripts([]Script{}, ""); got != "" {
		t.Errorf("WrapScripts([]Script{}) = %q, want empty string", got)
	}
}

func TestWrapScriptsSingleScript(t *testing.T) {
	body := WrapScripts([]Script{
		{Name: "xfce", Content: "#!/bin/sh\napt-get install -y xfce4\n"},
	}, "")

	f := parseWrapped(t, body)
	if len(f.WriteFiles) != 1 {
		t.Fatalf("write_files has %d entries, want 1:\n%s", len(f.WriteFiles), body)
	}
	wf := f.WriteFiles[0]
	if wf.Path != "/var/lib/stoat/recipes/xfce.sh" {
		t.Errorf("path = %q, want /var/lib/stoat/recipes/xfce.sh", wf.Path)
	}
	if wf.Permissions != "0755" {
		t.Errorf("permissions = %q, want 0755", wf.Permissions)
	}
	if !strings.Contains(wf.Content, "apt-get install -y xfce4") {
		t.Errorf("content = %q, missing script body", wf.Content)
	}
	if !strings.HasPrefix(wf.Content, "#!/bin/sh") {
		t.Errorf("content = %q, want it to start with the shebang", wf.Content)
	}

	if len(f.Runcmd) != 1 {
		t.Fatalf("runcmd has %d entries, want 1:\n%s", len(f.Runcmd), body)
	}
	wantCmd := "/var/lib/stoat/recipes/xfce.sh && mkdir -p /var/lib/stoat/.applied && touch /var/lib/stoat/.applied/xfce"
	if f.Runcmd[0] != wantCmd {
		t.Errorf("runcmd[0] = %q, want %q", f.Runcmd[0], wantCmd)
	}
}

// TestWrapScriptsPreservesOrder pins that execution order survives. The spec
// requires provision-stage scripts to run in selection order, so a caller
// that hands WrapScripts a dependency-ordered list must get that same order
// back in both write_files and runcmd.
func TestWrapScriptsPreservesOrder(t *testing.T) {
	body := WrapScripts([]Script{
		{Name: "base", Content: "echo base\n"},
		{Name: "docker", Content: "echo docker\n"},
		{Name: "xfce", Content: "echo xfce\n"},
	}, "")

	f := parseWrapped(t, body)
	wantPaths := []string{
		"/var/lib/stoat/recipes/base.sh",
		"/var/lib/stoat/recipes/docker.sh",
		"/var/lib/stoat/recipes/xfce.sh",
	}

	if len(f.WriteFiles) != len(wantPaths) {
		t.Fatalf("write_files has %d entries, want %d:\n%s", len(f.WriteFiles), len(wantPaths), body)
	}
	for i, want := range wantPaths {
		if f.WriteFiles[i].Path != want {
			t.Errorf("write_files[%d].Path = %q, want %q", i, f.WriteFiles[i].Path, want)
		}
	}

	if len(f.Runcmd) != len(wantPaths) {
		t.Fatalf("runcmd has %d entries, want %d:\n%s", len(f.Runcmd), len(wantPaths), body)
	}
	names := []string{"base", "docker", "xfce"}
	for i, path := range wantPaths {
		want := path + " && mkdir -p /var/lib/stoat/.applied && touch /var/lib/stoat/.applied/" + names[i]
		if f.Runcmd[i] != want {
			t.Errorf("runcmd[%d] = %q, want %q", i, f.Runcmd[i], want)
		}
	}
}

// TestWrapScriptsMultilineContentSurvives guards the YAML block-scalar
// indentation. A script with blank lines, its own indentation, and no
// trailing newline must round-trip through the YAML parser unchanged
// (modulo the single trailing newline every script gets). A script silently
// truncated or reflowed at write_files time would fail at first boot with
// no useful error.
func TestWrapScriptsMultilineContentSurvives(t *testing.T) {
	script := "#!/bin/sh\nset -e\n\nif true; then\n  echo indented\nfi"
	body := WrapScripts([]Script{{Name: "multi", Content: script}}, "")

	f := parseWrapped(t, body)
	if len(f.WriteFiles) != 1 {
		t.Fatalf("write_files has %d entries, want 1:\n%s", len(f.WriteFiles), body)
	}
	got := strings.TrimRight(f.WriteFiles[0].Content, "\n")
	if got != script {
		t.Errorf("content round-trip mismatch:\ngot:\n%q\nwant:\n%q", got, script)
	}
}

// TestWrapScriptsFragmentMergesIntoSeed is the integration case.
// WrapScripts' output is meant to be passed to Seed exactly like any other
// recipe fragment, so it must survive withMergeHow/buildArchive the same
// way (see TestArchiveWriteFilesSurvives for the same contract on a
// hand-written fragment).
func TestWrapScriptsFragmentMergesIntoSeed(t *testing.T) {
	fragment := WrapScripts([]Script{{Name: "xfce", Content: "echo hi\n"}}, "")

	got := withMergeHow(fragment)
	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Errorf("merged fragment lost its #cloud-config header:\n%s", got)
	}
	if !strings.Contains(got, "write_files:") || !strings.Contains(got, "runcmd:") {
		t.Errorf("merged fragment lost write_files/runcmd:\n%s", got)
	}
	if !strings.Contains(got, "merge_how:") {
		t.Errorf("merged fragment missing merge_how directive:\n%s", got)
	}
}

// A non-empty prelude runs once as the first runcmd entry, via
// stoat_pkg_setup, and is prepended to every script's own content. This is
// how the cloudinit path keeps the package index refresh and the recipe
// verbs behaving the same as the ssh path.
func TestWrapScriptsRunsSetupFirst(t *testing.T) {
	got := WrapScripts([]Script{{Name: "x", Content: "#!/bin/sh\necho hi\n"}}, "P\n")
	if !strings.Contains(got, "runcmd:\n  - sh -c 'P\nstoat_pkg_setup'\n") {
		t.Errorf("setup not first in runcmd:\n%s", got)
	}
	if !strings.Contains(got, "      #!/bin/sh\n      P\n      echo hi\n") {
		t.Errorf("prelude not after the shebang:\n%s", got)
	}
}

func TestWrapScriptsNamespacesSecretsAndRemovesTheSecretFileLast(t *testing.T) {
	body := WrapScripts([]Script{
		{
			Name: "docker", Content: "#!/bin/sh\necho docker\n",
			Env:     []string{"STOAT_RECIPE=docker", "STOAT_PARAM_USER=dev"},
			Secrets: map[string]string{"authkey": "docker-secret"},
		},
		{
			Name: "tailscale", Content: "#!/bin/sh\necho tailscale\n",
			Env:     []string{"STOAT_RECIPE=tailscale"},
			Secrets: map[string]string{"authkey": "tailscale-secret"},
		},
	}, "")
	f := parseWrapped(t, body)

	var secretFile *struct {
		Path        string
		Permissions string
		Content     string
	}
	for i := range f.WriteFiles {
		wf := f.WriteFiles[i]
		if wf.Path == SecretsEnvPath {
			copy := struct {
				Path        string
				Permissions string
				Content     string
			}{wf.Path, wf.Permissions, wf.Content}
			secretFile = &copy
		}
	}
	if secretFile == nil {
		t.Fatalf("no %s write_files entry:\n%s", SecretsEnvPath, body)
	}
	if secretFile.Permissions != "0600" {
		t.Errorf("secrets permissions = %q, want 0600", secretFile.Permissions)
	}
	for _, want := range []string{
		"STOAT_PARAM_DOCKER_AUTHKEY", "docker-secret",
		"STOAT_PARAM_TAILSCALE_AUTHKEY", "tailscale-secret",
	} {
		if !strings.Contains(secretFile.Content, want) {
			t.Errorf("secret file missing %q:\n%s", want, secretFile.Content)
		}
	}
	if len(f.Runcmd) != 3 {
		t.Fatalf("runcmd = %v, want two recipes plus final cleanup", f.Runcmd)
	}
	if !strings.Contains(f.Runcmd[0], "STOAT_PARAM_DOCKER_AUTHKEY") || !strings.Contains(f.Runcmd[1], "STOAT_PARAM_TAILSCALE_AUTHKEY") {
		t.Errorf("recipe wrappers do not select their namespaced secrets: %v", f.Runcmd[:2])
	}
	if got := f.Runcmd[len(f.Runcmd)-1]; got != "rm -f "+SecretsEnvPath {
		t.Errorf("last runcmd = %q, want secret cleanup", got)
	}
}

func TestWrapScriptsWithoutSecretsWritesNoSecretFile(t *testing.T) {
	f := parseWrapped(t, WrapScripts([]Script{{Name: "xfce", Content: "#!/bin/sh\n"}}, ""))
	for _, wf := range f.WriteFiles {
		if wf.Path == SecretsEnvPath {
			t.Fatalf("a recipe with no secrets wrote %s", SecretsEnvPath)
		}
	}
	for _, cmd := range f.Runcmd {
		if strings.Contains(cmd, SecretsEnvPath) {
			t.Fatalf("a recipe with no secrets references %s: %q", SecretsEnvPath, cmd)
		}
	}
}

func TestWrapScriptsFailureCannotWriteSuccessMarker(t *testing.T) {
	f := parseWrapped(t, WrapScripts([]Script{{
		Name: "docker", Content: "#!/bin/sh\nexit 1\n",
		Env: []string{"STOAT_RECIPE=docker"},
	}}, ""))
	if len(f.Runcmd) != 1 {
		t.Fatalf("runcmd = %v, want one recipe command", f.Runcmd)
	}
	cmd := f.Runcmd[0]
	marker := MarkerDir + "/docker"
	if !strings.Contains(cmd, "/tmp/.stoat-out/docker") {
		t.Errorf("recipe output was not copied before success marking: %q", cmd)
	}
	markerAt := strings.Index(cmd, marker)
	if markerAt < 0 || !strings.Contains(cmd[:markerAt], "&&") {
		t.Errorf("success marker is not gated by the recipe/output commands: %q", cmd)
	}
	if strings.Contains(cmd, "; touch "+marker) {
		t.Errorf("success marker is unconditional after a semicolon: %q", cmd)
	}
}
