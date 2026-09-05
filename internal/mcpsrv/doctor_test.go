package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorReportsTheContractAndTheClients(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, t.TempDir())

	r := DoctorReport("test")
	if r.Contract != Contract {
		t.Fatalf("Contract = %d, want %d", r.Contract, Contract)
	}
	if r.Transport != "stdio" {
		t.Fatalf("Transport = %q", r.Transport)
	}
	if len(r.Clients) != 4 {
		t.Fatalf("got %d clients, want 4", len(r.Clients))
	}
	for _, c := range r.Clients {
		if c.Installed {
			t.Errorf("%s: reported installed with no config file", c.Client)
		}
	}

	if _, err := Install("cursor", InstallOpts{}); err != nil {
		t.Fatal(err)
	}
	r = DoctorReport("test")
	for _, c := range r.Clients {
		if c.Client != "cursor" {
			continue
		}
		if !c.Installed {
			t.Fatal("cursor is not reported as installed")
		}
		// The entry must point at the binary that is running, or the client
		// launches a different stoat than the one that wrote the entry.
		if !c.Current {
			t.Fatalf("cursor entry points at %q, not the running binary", c.Command)
		}
	}
	_ = filepath.Join
	_ = os.Stat
}
