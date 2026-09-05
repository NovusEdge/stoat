package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/tomlx"
)

func TestScopeForProjectWhenStoatTomlIsPresent(t *testing.T) {
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), "[recipes]\n")
	t.Chdir(wd)

	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "project" {
		t.Errorf("Name = %q, want %q", s.Name, "project")
	}
	if s.LockPath != filepath.Join(wd, "stoat.lock") {
		t.Errorf("LockPath = %q", s.LockPath)
	}
	if s.CachePath != filepath.Join(wd, ".stoat", "recipes") {
		t.Errorf("CachePath = %q", s.CachePath)
	}
}

func TestScopeForGlobalFlagOverridesTheProject(t *testing.T) {
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), "[recipes]\n")
	t.Chdir(wd)
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)

	s, err := ScopeFor(true)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "global" || s.LockPath != filepath.Join(home, "stoat.lock") {
		t.Errorf("scope = %+v, want the home scope", s)
	}

	parent := t.TempDir()
	writeFile(t, filepath.Join(parent, "stoat.toml"), "[recipes]\n")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	s, err = ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "global" {
		t.Errorf("scope = %q, want global when only a parent has stoat.toml", s.Name)
	}
}

func TestDeclsAcceptAStringOrATable(t *testing.T) {
	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), `[recipes]
tailscale = "v1.2"
xfce = { source = "https://github.com/x/stoat-xfce", ref = "main" }
`)
	t.Chdir(wd)

	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Decls()
	if err != nil {
		t.Fatal(err)
	}
	if d["tailscale"] != (Decl{Ref: "v1.2"}) {
		t.Errorf("tailscale = %+v", d["tailscale"])
	}
	if d["xfce"] != (Decl{Source: "https://github.com/x/stoat-xfce", Ref: "main"}) {
		t.Errorf("xfce = %+v", d["xfce"])
	}
}

func TestSetDeclAndRemoveDecl(t *testing.T) {
	type projectDocument struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
		Recipes map[string]string `toml:"recipes"`
	}

	wd := t.TempDir()
	writeFile(t, filepath.Join(wd, "stoat.toml"), `[project]
name = "alpha"

[recipes]
`)
	t.Chdir(wd)
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetDecl("tailscale", Decl{Ref: "v1.2"}); err != nil {
		t.Fatal(err)
	}
	checkProjectData := func() {
		t.Helper()
		var project projectDocument
		if err := tomlx.Decode(filepath.Join(wd, "stoat.toml"), &project, tomlx.Reject); err != nil {
			t.Fatal(err)
		}
		if project.Project.Name != "alpha" {
			t.Fatalf("unrelated project data was lost: %+v", project)
		}
	}
	checkProjectData()
	d, err := s.Decls()
	if err != nil {
		t.Fatal(err)
	}
	if d["tailscale"].Ref != "v1.2" {
		t.Fatalf("decls = %+v", d)
	}
	if err := s.RemoveDecl("tailscale"); err != nil {
		t.Fatal(err)
	}
	d, err = s.Decls()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d["tailscale"]; ok {
		t.Errorf("decls = %+v, want tailscale gone", d)
	}
	checkProjectData()
}

func TestSetDeclAndSavePreserveExistingFileModes(t *testing.T) {
	wd := t.TempDir()
	configPath := filepath.Join(wd, "stoat.toml")
	writeFile(t, configPath, "[recipes]\n")
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	s, err := ScopeFor(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"old": {Source: "source", Ref: "main", Commit: "old", Added: "now"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.LockPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDecl("tailscale", Decl{Ref: "v1.2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Lock{Schema: LockSchema, Recipes: map[string]LockEntry{
		"new": {Source: "source", Ref: "main", Commit: "new", Added: "now"},
	}}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		configPath: 0o644,
		s.LockPath: 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
}

func TestIgnoreStoatDirAppendsOnceInAGitCheckout(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wd, ".gitignore"), "build/\n")

	for range 2 {
		if err := IgnoreStoatDir(wd); err != nil {
			t.Fatal(err)
		}
	}
	got := readFile(t, filepath.Join(wd, ".gitignore"))
	if strings.Count(got, ".stoat/") != 1 {
		t.Errorf("gitignore = %q, want one .stoat/ line", got)
	}
}

func TestIgnoreStoatDirSkipsANonCheckout(t *testing.T) {
	wd := t.TempDir()
	if err := IgnoreStoatDir(wd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("gitignore was created outside a checkout")
	}
}
