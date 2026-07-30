package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveLoadRoundtrip(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	want := &VM{
		Name:      "alpine-live",
		Mode:      "live",
		ISO:       "isos/alpine-standard-3.24.1-x86_64.iso",
		RAM:       4096,
		CPUs:      4,
		Disk:      "8G",
		Installed: true,
		Share:     "/home/someone/vms",
		SSHPort:   2201,
		Recipes:   []string{"xfce"},
		Dir:       filepath.Join(Root(), "alpine-live"),
	}
	if err := want.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load("alpine-live")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("roundtrip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestListSorted(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"zeta", "alpha"} {
		v := &VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, Dir: filepath.Join(Root(), n)}
		if err := v.Save(); err != nil {
			t.Fatal(err)
		}
	}
	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 || vms[0].Name != "alpha" || vms[1].Name != "zeta" {
		t.Errorf("want [alpha zeta], got %v", vms)
	}
}

func TestFreePortSkipsClaimedPort(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	v := &VM{Name: "claimed", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Dir: filepath.Join(Root(), "claimed")}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p == 2200 {
		t.Errorf("FreePort returned 2200 even though it is claimed by %q", v.Name)
	}
}

func TestFreePortNoCollisionAcrossSequentialCreates(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := EnsureRoot(); err != nil {
		t.Fatal(err)
	}

	p1, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	v1 := &VM{Name: "first", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: p1, Dir: filepath.Join(Root(), "first")}
	if err := v1.Save(); err != nil {
		t.Fatal(err)
	}

	p2, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p2 == p1 {
		t.Errorf("second FreePort() call returned the same port as the first: %d", p1)
	}
}

func TestFreePortFreshInstallNoVMs(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	// Deliberately do not call EnsureRoot: List() will fail to read the
	// root, and FreePort must still succeed rather than treating that as
	// "every port is claimed".
	p, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	if p != 2200 {
		t.Errorf("want first free port 2200 on a fresh install, got %d", p)
	}
}
