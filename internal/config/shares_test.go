package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A project VM round-trips its project path and its shares through vm.toml.
func TestProjectAndSharesRoundTrip(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &VM{
		Name: "myrepo-dev", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200,
		Project: "/home/u/myrepo",
		Shares: []Share{
			{Tag: "p0", Host: "/home/u/myrepo", Guest: "/work"},
			{Tag: "p1", Host: "/home/u/myrepo/src", Guest: "/work/src"},
		},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "/home/u/myrepo" {
		t.Errorf("project = %q, want /home/u/myrepo", got.Project)
	}
	if len(got.Shares) != 2 || got.Shares[1].Guest != "/work/src" {
		t.Errorf("shares = %+v", got.Shares)
	}
	if _, err := os.Stat(filepath.Join(Root(), "myrepo-dev", "vm.toml")); err != nil {
		t.Fatal(err)
	}
}
