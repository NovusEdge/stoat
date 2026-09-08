package hostops

import (
	"errors"
	"runtime"
	"strings"
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
	if !strings.Contains(err.Error(), runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("RequireLocalHypervisor() = %v, want message to name %s/%s", err, runtime.GOOS, runtime.GOARCH)
	}
}
