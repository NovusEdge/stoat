package recipes

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeManifestFile writes contents to <dir>/recipe.toml and returns its path.
func writeManifestFile(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "recipe.toml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseManifestFullBlock(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "xfce"
description = "XFCE desktop environment with lightdm"
version = "1.0.0"
os = ["alpine", "ubuntu"]
requires = ["systemd"]
stage = "install"
script = "install.sh"
auto = true
run = "always"
reboot = true

[scripts]
alpine = "install-alpine.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "xfce" {
		t.Errorf("Name = %q, want xfce", m.Name)
	}
	if m.Description != "XFCE desktop environment with lightdm" {
		t.Errorf("Description = %q", m.Description)
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q, want 1.0.0", m.Version)
	}
	if !slices.Equal(m.OS, []string{"alpine", "ubuntu"}) {
		t.Errorf("OS = %v", m.OS)
	}
	if !slices.Equal(m.Requires, []string{"systemd"}) {
		t.Errorf("Requires = %v", m.Requires)
	}
	if m.Stage != "install" {
		t.Errorf("Stage = %q, want install", m.Stage)
	}
	if m.Script != "install.sh" {
		t.Errorf("Script = %q, want install.sh", m.Script)
	}
	if !m.Auto {
		t.Error("Auto = false, want true")
	}
	if m.Run != "always" {
		t.Errorf("Run = %q, want always", m.Run)
	}
	if m.Scripts["alpine"] != "install-alpine.sh" {
		t.Errorf("Scripts[alpine] = %q, want install-alpine.sh", m.Scripts["alpine"])
	}
	if !m.Reboot {
		t.Error("Reboot = false, want true")
	}
}

func TestParseManifestDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Stage != "provision" {
		t.Errorf("Stage = %q, want default provision", m.Stage)
	}
	if m.Run != "once" {
		t.Errorf("Run = %q, want default once", m.Run)
	}
	if m.Auto {
		t.Error("Auto = true, want default false")
	}
	if m.Reboot {
		t.Error("Reboot = true, want default false")
	}
}

func TestParseManifestMissingName(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `script = "install.sh"`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `missing required field "name"`) {
		t.Errorf("err = %v, want a missing-name error", err)
	}
}

func TestParseManifestMissingScript(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `name = "docker"`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `missing required field "script"`) {
		t.Errorf("err = %v, want a missing-script error", err)
	}
}

func TestParseManifestBadStage(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
stage = "orbit"
`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `invalid stage "orbit"`) {
		t.Errorf("err = %v, want an invalid-stage error", err)
	}
}

func TestParseManifestBadRun(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
run = "sometimes"
`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `invalid run "sometimes"`) {
		t.Errorf("err = %v, want an invalid-run error", err)
	}
}

func TestParseManifestNotFound(t *testing.T) {
	_, err := ParseManifest(filepath.Join(t.TempDir(), "missing", "recipe.toml"))
	if err == nil {
		t.Error("want an error for a missing manifest file")
	}
}

func TestParseManifestBadTOML(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `name = "docker`)
	_, err := ParseManifest(path)
	if err == nil {
		t.Error("want an error for malformed TOML")
	}
}

func TestManifestScriptForDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "install.sh")
	if got := m.ScriptFor("ubuntu"); got != want {
		t.Errorf("ScriptFor(ubuntu) = %q, want %q", got, want)
	}
}

func TestManifestScriptForOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "xfce"
script = "install.sh"

[scripts]
alpine = "install-alpine.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "install-alpine.sh"); m.ScriptFor("alpine") != want {
		t.Errorf("ScriptFor(alpine) = %q, want %q", m.ScriptFor("alpine"), want)
	}
	if want := filepath.Join(dir, "install.sh"); m.ScriptFor("ubuntu") != want {
		t.Errorf("ScriptFor(ubuntu) = %q, want %q (falls back to default)", m.ScriptFor("ubuntu"), want)
	}
}

func TestManifestScriptContent(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"

[scripts]
alpine = "install-alpine.sh"
`)
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\necho default\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install-alpine.sh"), []byte("#!/bin/sh\necho alpine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := m.ScriptContent("ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "echo default") {
		t.Errorf("ScriptContent(ubuntu) = %q, want the default script", got)
	}

	got, err = m.ScriptContent("alpine")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "echo alpine") {
		t.Errorf("ScriptContent(alpine) = %q, want the alpine override", got)
	}
}

func TestMatchesVMEmptyOSMatchesAll(t *testing.T) {
	m := &Manifest{Name: "docker"}
	for _, vmOS := range []string{"ubuntu", "alpine", "arch", "fedora", "debian"} {
		if !MatchesVM(m, vmOS) {
			t.Errorf("MatchesVM(%q) = false, want true for an empty OS list", vmOS)
		}
	}
}

func TestMatchesVMOSFiltering(t *testing.T) {
	m := &Manifest{Name: "xfce", OS: []string{"alpine", "ubuntu"}}
	if !MatchesVM(m, "alpine") {
		t.Error("MatchesVM(alpine) = false, want true, alpine is in OS")
	}
	if !MatchesVM(m, "ubuntu") {
		t.Error("MatchesVM(ubuntu) = false, want true, ubuntu is in OS")
	}
	if MatchesVM(m, "arch") {
		t.Error("MatchesVM(arch) = true, want false, arch is not in OS")
	}
}

func TestMatchesVMRequiresCapability(t *testing.T) {
	m := &Manifest{Name: "svc", Requires: []string{"systemd"}}
	if !MatchesVM(m, "ubuntu") {
		t.Error("MatchesVM(ubuntu) = false, want true, ubuntu has systemd")
	}
	if MatchesVM(m, "alpine") {
		t.Error("MatchesVM(alpine) = true, want false, alpine uses openrc not systemd")
	}
}

func TestMatchesVMMultipleCapabilities(t *testing.T) {
	m := &Manifest{Name: "svc", Requires: []string{"systemd", "apt"}}
	if !MatchesVM(m, "ubuntu") {
		t.Error("MatchesVM(ubuntu) = false, want true, ubuntu has both systemd and apt")
	}
	if MatchesVM(m, "fedora") {
		t.Error("MatchesVM(fedora) = true, want false, fedora has systemd but not apt")
	}
	if MatchesVM(m, "alpine") {
		t.Error("MatchesVM(alpine) = true, want false, alpine has neither")
	}
}

func TestMatchesVMUnknownCapability(t *testing.T) {
	m := &Manifest{Name: "svc", Requires: []string{"nonexistent"}}
	if MatchesVM(m, "ubuntu") {
		t.Error("MatchesVM(ubuntu) = true, want false, no OS satisfies an unknown capability")
	}
}

func TestMatchesVMOSAndCapabilityCombined(t *testing.T) {
	m := &Manifest{Name: "svc", OS: []string{"ubuntu", "alpine"}, Requires: []string{"systemd"}}
	if !MatchesVM(m, "ubuntu") {
		t.Error("MatchesVM(ubuntu) = false, want true, in OS list and has systemd")
	}
	if MatchesVM(m, "alpine") {
		t.Error("MatchesVM(alpine) = true, want false, in OS list but lacks systemd")
	}
	if MatchesVM(m, "fedora") {
		t.Error("MatchesVM(fedora) = true, want false, not in OS list")
	}
}

func TestManifestScriptContentMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ScriptContent("ubuntu"); err == nil {
		t.Error("want an error, install.sh does not exist on disk")
	}
}
