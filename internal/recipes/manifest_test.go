package recipes

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseManifestRejectsUnknownKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "recipe.toml")
	if err := os.WriteFile(p, []byte("name = \"x\"\nscript = \"i.sh\"\nrunn = \"once\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseManifest(p)
	if err == nil || !strings.Contains(err.Error(), `unknown key "runn"`) {
		t.Errorf("err = %v", err)
	}
}

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

// TestParseManifestDependsField pins that depends parses into a string slice,
// and defaults to empty when absent.
func TestParseManifestDependsField(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "devtools"
script = "install.sh"
depends = ["docker", "tailscale"]
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(m.Depends, []string{"docker", "tailscale"}) {
		t.Errorf("Depends = %v, want [docker tailscale]", m.Depends)
	}

	bare := writeManifestFile(t, t.TempDir(), "name = \"solo\"\nscript = \"install.sh\"\n")
	m, err = ParseManifest(bare)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Depends) != 0 {
		t.Errorf("Depends = %v, want empty when the field is absent", m.Depends)
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
	if m.Runtime != "sh" {
		t.Errorf("Runtime = %q, want default sh", m.Runtime)
	}
}

func TestParseManifestRuntimePython3(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "pyrecipe"
script = "install.py"
runtime = "python3"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Runtime != "python3" {
		t.Errorf("Runtime = %q, want python3", m.Runtime)
	}
}

func TestParseManifestBadRuntime(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "docker"
script = "install.sh"
runtime = "perl"
`)
	_, err := ParseManifest(path)
	if err == nil || !strings.Contains(err.Error(), `invalid runtime "perl"`) {
		t.Errorf("err = %v, want an invalid-runtime error", err)
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

// ubuntu's guest.toml declares the alias "debian-family". ScriptFor must
// try it before falling back to the manifest's default script.
func TestManifestScriptForTriesGuestAlias(t *testing.T) {
	dir := t.TempDir()
	path := writeManifestFile(t, dir, `
name = "install"
script = "install.sh"

[scripts]
debian-family = "install-deb.sh"
`)
	m, err := ParseManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "install-deb.sh"); m.ScriptFor("ubuntu") != want {
		t.Errorf("ScriptFor(ubuntu) = %q, want %q via the debian-family alias", m.ScriptFor("ubuntu"), want)
	}
	if want := filepath.Join(dir, "install.sh"); m.ScriptFor("fedora") != want {
		t.Errorf("ScriptFor(fedora) = %q, want the default, fedora has no matching alias", m.ScriptFor("fedora"))
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

// hasCapability now resolves against guest.Capabilities() instead of a
// hardcoded map. Pin the per-package-manager capabilities the old map held,
// since MatchesVM's tests above only exercise systemd/openrc/apt.
func TestMatchesVMPackageManagerCapabilities(t *testing.T) {
	cases := []struct {
		cap, vmOS string
		want      bool
	}{
		{"apk", "alpine", true},
		{"apk", "ubuntu", false},
		{"dnf", "fedora", true},
		{"dnf", "arch", false},
		{"pacman", "arch", true},
		{"pacman", "fedora", false},
	}
	for _, c := range cases {
		m := &Manifest{Name: "svc", Requires: []string{c.cap}}
		if got := MatchesVM(m, c.vmOS); got != c.want {
			t.Errorf("MatchesVM with requires=%q on %s = %v, want %v", c.cap, c.vmOS, got, c.want)
		}
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
