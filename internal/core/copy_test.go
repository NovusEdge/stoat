package core

import (
	"context"
	"errors"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestCopyToRejectsEmptyPaths(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ local, remote string }{
		{"", "/root/f"},
		{"/tmp/f", ""},
		{"  ", "/root/f"},
	} {
		if err := CopyTo(context.Background(), "work", tc.local, tc.remote); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("CopyTo(%q, %q) err = %v, want ErrInvalidSpec", tc.local, tc.remote, err)
		}
		if err := CopyFrom(context.Background(), "work", tc.remote, tc.local); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("CopyFrom(%q, %q) err = %v, want ErrInvalidSpec", tc.remote, tc.local, err)
		}
	}
}

func TestCopyToUnknownVM(t *testing.T) {
	root(t)
	if err := CopyTo(context.Background(), "nope", "/tmp/f", "/root/f"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCopyFromUnknownVM(t *testing.T) {
	root(t)
	if err := CopyFrom(context.Background(), "nope", "/root/f", "/tmp/f"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A stopped VM must be refused before scp is ever dialled — see doCopy's
// comment, and Exec's identical reasoning in exec_test.go.
func TestCopyToStoppedVMIsNotRunning(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := CopyTo(context.Background(), "work", "/tmp/f", "/root/f"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestCopyFromStoppedVMIsNotRunning(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := CopyFrom(context.Background(), "work", "/root/f", "/tmp/f"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestCopyToBrokenVM(t *testing.T) {
	root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")
	if err := CopyTo(context.Background(), "hosed", "/tmp/f", "/root/f"); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v, want ErrBroken", err)
	}
}

func TestCopyFromBrokenVM(t *testing.T) {
	root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")
	if err := CopyFrom(context.Background(), "hosed", "/root/f", "/tmp/f"); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v, want ErrBroken", err)
	}
}
