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
	if got := WrapScripts(nil); got != "" {
		t.Errorf("WrapScripts(nil) = %q, want empty string", got)
	}
	if got := WrapScripts([]Script{}); got != "" {
		t.Errorf("WrapScripts([]Script{}) = %q, want empty string", got)
	}
}

func TestWrapScriptsSingleScript(t *testing.T) {
	body := WrapScripts([]Script{
		{Name: "xfce", Content: "#!/bin/sh\napt-get install -y xfce4\n"},
	})

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
	if f.Runcmd[0] != "/var/lib/stoat/recipes/xfce.sh" {
		t.Errorf("runcmd[0] = %q, want /var/lib/stoat/recipes/xfce.sh", f.Runcmd[0])
	}
}

// TestWrapScriptsPreservesOrder pins that execution order is preserved: the
// spec is explicit that provision-stage scripts run in selection order, so a
// caller that hands WrapScripts a dependency-ordered list must get that same
// order back in both write_files and runcmd.
func TestWrapScriptsPreservesOrder(t *testing.T) {
	body := WrapScripts([]Script{
		{Name: "base", Content: "echo base\n"},
		{Name: "docker", Content: "echo docker\n"},
		{Name: "xfce", Content: "echo xfce\n"},
	})

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
	for i, want := range wantPaths {
		if f.Runcmd[i] != want {
			t.Errorf("runcmd[%d] = %q, want %q", i, f.Runcmd[i], want)
		}
	}
}

// TestWrapScriptsMultilineContentSurvives guards the YAML block-scalar
// indentation: a script with blank lines, indentation of its own, and no
// trailing newline must round-trip through the YAML parser unchanged
// (module the single trailing newline every script gets), since a script
// silently truncated or reflowed at write_files time would fail at
// first-boot with no useful error.
func TestWrapScriptsMultilineContentSurvives(t *testing.T) {
	script := "#!/bin/sh\nset -e\n\nif true; then\n  echo indented\nfi"
	body := WrapScripts([]Script{{Name: "multi", Content: script}})

	f := parseWrapped(t, body)
	if len(f.WriteFiles) != 1 {
		t.Fatalf("write_files has %d entries, want 1:\n%s", len(f.WriteFiles), body)
	}
	got := strings.TrimRight(f.WriteFiles[0].Content, "\n")
	if got != script {
		t.Errorf("content round-trip mismatch:\ngot:\n%q\nwant:\n%q", got, script)
	}
}

// TestWrapScriptsFragmentMergesIntoSeed is the integration case: WrapScripts'
// output is meant to be passed to Seed exactly like any other recipe
// fragment, so it must survive withMergeHow/buildArchive the same way
// (see TestArchiveWriteFilesSurvives for the same contract on a hand-written
// fragment).
func TestWrapScriptsFragmentMergesIntoSeed(t *testing.T) {
	fragment := WrapScripts([]Script{{Name: "xfce", Content: "echo hi\n"}})

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
