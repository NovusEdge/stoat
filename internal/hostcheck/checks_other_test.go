//go:build !linux

package hostcheck

import (
	"runtime"
	"strings"
	"testing"
)

func TestHostCheckReportsUnsupportedNativeHost(t *testing.T) {
	checks := RunChecks(DistroArch)
	if len(checks) != 1 {
		t.Fatalf("RunChecks() returned %d rows: %+v; want one host-support row", len(checks), checks)
	}
	check := checks[0]
	if check.OK {
		t.Fatalf("unsupported host row is OK: %+v", check)
	}
	if check.Optional {
		t.Fatalf("unsupported host row is optional: %+v", check)
	}
	if check.Name == "" {
		t.Fatal("unsupported host row has no name")
	}
	if len(check.Fix) != 0 {
		t.Fatalf("unsupported host row offers Linux package guidance: %v", check.Fix)
	}
	row := strings.ToLower(check.Name + " " + check.Detail)
	for _, linuxAssumption := range []string{"/dev/kvm", "qemu-system-x86_64", "pacman", "apt", "dnf"} {
		if strings.Contains(row, strings.ToLower(linuxAssumption)) {
			t.Errorf("unsupported host row claims Linux requirement %q: %+v", linuxAssumption, check)
		}
	}
	wantHost := runtime.GOOS + "/" + runtime.GOARCH
	if !strings.Contains(check.Detail, wantHost) {
		t.Errorf("unsupported host detail = %q, want it to identify %s", check.Detail, wantHost)
	}
}
