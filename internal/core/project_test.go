package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/project"
)

// projectDir writes a stoat.toml into a fresh directory, chdirs there and
// returns the loaded project. Every project test runs from inside the project
// directory, the same place a user runs stoat from.
func projectDir(t *testing.T, body string) *project.Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const fullDecl = `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`

func TestSpecForFullDeclaration(t *testing.T) {
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "myrepo-dev" {
		t.Errorf("name = %q, want myrepo-dev", s.Name)
	}
	if s.Image != "alpine-virt" || s.CPUs != 2 || s.RAM != 2048 {
		t.Errorf("spec = %+v", s)
	}
	if s.Project != p.Dir {
		t.Errorf("project = %q, want %q", s.Project, p.Dir)
	}
	if len(s.Shares) != 1 || s.Shares[0].Guest != "/work" {
		t.Errorf("shares = %+v", s.Shares)
	}
	if s.Params["docker"]["user"] != "dev" {
		t.Errorf("params = %v", s.Params)
	}
	if s.AgentAccess != "observe" {
		t.Errorf("agent_access = %q, want observe", s.AgentAccess)
	}
}

// A minimal declaration takes stoat new's defaults for everything but image.
func TestSpecForMinimalDeclaration(t *testing.T) {
	p := projectDir(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.ci]\nimage = \"alpine-virt\"\n")
	s, err := SpecFor(p, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "myrepo-ci" || s.Image != "alpine-virt" {
		t.Errorf("spec = %+v", s)
	}
	if s.RAM != 0 || s.CPUs != 0 || s.Disk != "" {
		t.Errorf("spec sets a default it should leave to plan(): %+v", s)
	}
	if len(s.Shares) != 0 {
		t.Errorf("shares = %+v, want none", s.Shares)
	}
}

// The vm.toml a declaration produces is the golden this whole feature rests
// on: a contributor's stoat up must build the same VM the author's did.
func TestSpecForWritesTheExpectedVMToml(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "myrepo-dev" || v.RAM != 2048 || v.CPUs != 2 {
		t.Errorf("vm = %+v", v)
	}
	if v.Project != p.Dir {
		t.Errorf("project = %q, want %q", v.Project, p.Dir)
	}
	if len(v.Shares) != 1 || v.Shares[0].Tag != "p0" || v.Shares[0].Guest != "/work" {
		t.Errorf("shares = %+v", v.Shares)
	}
}
