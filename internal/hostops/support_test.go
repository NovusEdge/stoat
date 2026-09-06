//go:build !linux

package hostops

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRequireVMUnsupportedHost(t *testing.T) {
	err := RequireVM()
	if err == nil {
		t.Fatal("RequireVM() = nil on an unqualified native host")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("RequireVM() = %v, want errors.Is(..., ErrUnsupported)", err)
	}
	wantHost := runtime.GOOS + "/" + runtime.GOARCH
	if !strings.Contains(err.Error(), wantHost) {
		t.Errorf("RequireVM() = %q, want it to identify %s", err, wantHost)
	}
}
