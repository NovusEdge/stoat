package project

import (
	"os"
	"path/filepath"
	"testing"
)

// write drops a stoat.toml into a fresh directory and returns the directory.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const full = `
schema = 1

[project]
name = "myrepo"

[recipes]
tailscale = "v1.2"

[vms.dev]
name    = "shared-dev"
image   = "ubuntu-24"
cpus    = 4
ram     = 4096
disk    = "20G"
recipes = ["docker", "tailscale"]
shares  = ["."]
agent_access = "manage"

[vms.dev.params.docker]
user = "dev"

[vms.ci]
image = "alpine-virt"
`

func TestLoadFullDeclaration(t *testing.T) {
	p, err := Load(write(t, full))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "myrepo" {
		t.Errorf("name = %q, want myrepo", p.Name)
	}
	if len(p.VMs) != 2 || p.VMs[0].Key != "dev" || p.VMs[1].Key != "ci" {
		t.Fatalf("vms = %+v, want dev then ci in declaration order", p.VMs)
	}
	dev := p.VMs[0]
	if dev.Image != "ubuntu-24" || dev.CPUs != 4 || dev.RAM != 4096 || dev.Disk != "20G" {
		t.Errorf("dev = %+v", dev)
	}
	if got := dev.Params["docker"]["user"]; got != "dev" {
		t.Errorf("params.docker.user = %v, want dev", got)
	}
}

// The project name defaults to the directory it was loaded from, so a
// clone of a repository with no [project] table still names its VMs.
func TestProjectNameDefaultsToDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "MyRepo")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, FileName), []byte("schema = 1\n\n[vms.dev]\nimage = \"alpine-virt\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "myrepo" {
		t.Errorf("name = %q, want myrepo", p.Name)
	}
}

func TestGlobalNameAndResolve(t *testing.T) {
	p, err := Load(write(t, full))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.GlobalName("dev"); got != "shared-dev" {
		t.Errorf("GlobalName(dev) = %q, want shared-dev", got)
	}
	if got := p.GlobalName("ci"); got != "myrepo-ci" {
		t.Errorf("GlobalName(ci) = %q, want myrepo-ci", got)
	}
	for _, tc := range []struct{ arg, want string }{
		{"dev", "shared-dev"},        // by key
		{"shared-dev", "shared-dev"}, // by global name
		{"myrepo-ci", "myrepo-ci"},
	} {
		got, ok := p.Resolve(tc.arg)
		if !ok || got != tc.want {
			t.Errorf("Resolve(%q) = %q,%v, want %q,true", tc.arg, got, ok, tc.want)
		}
	}
	if _, ok := p.Resolve("db"); ok {
		t.Error("Resolve(db) succeeded, want not found")
	}
}

func TestDuplicateGlobalNameIsAnError(t *testing.T) {
	dir := write(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"a\"\n\n[vms.ci]\nname = \"myrepo-dev\"\nimage = \"b\"\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load succeeded, want a duplicate-name error")
	}
	want := `stoat.toml: vms.ci and vms.dev both resolve to "myrepo-dev"`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\ncpu = 4\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted an unknown field; tomlx.Reject is not in force")
	}
}

func TestBadKeyIsRejected(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.Dev_1]\nimage = \"a\"\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("Load accepted an out-of-grammar vm key")
	}
}

func TestFindUsesTheCurrentDirectoryOnly(t *testing.T) {
	dir := write(t, full)
	t.Chdir(dir)
	p, ok, err := Find()
	if err != nil || !ok || p.Name != "myrepo" {
		t.Fatalf("Find() = %v,%v,%v in the project dir", p, ok, err)
	}

	sub := filepath.Join(dir, "cmd")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if _, ok, err := Find(); ok || err != nil {
		t.Errorf("Find() = %v,%v in a subdirectory; there is no walk-up", ok, err)
	}
}
