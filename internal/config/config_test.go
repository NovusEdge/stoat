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
