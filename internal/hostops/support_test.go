package hostops

import (
	"errors"
	"runtime"
	"testing"
)

func TestRequireDataRootAlwaysAllows(t *testing.T) {
	if err := RequireDataRoot(); err != nil {
		t.Errorf("RequireDataRoot() = %v, want nil on every platform", err)
	}
}

func TestRequireLocalHypervisorFollowsPlatform(t *testing.T) {
	err := RequireLocalHypervisor()
	if runtime.GOOS == "linux" {
		if err != nil {
			t.Errorf("RequireLocalHypervisor() = %v, want nil on linux", err)
		}
		return
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("RequireLocalHypervisor() = %v, want ErrUnsupported", err)
	}
}
