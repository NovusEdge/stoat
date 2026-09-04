package recipes

import (
	"strings"
	"testing"
)

func TestBootstrapScriptShIsEmpty(t *testing.T) {
	if got := BootstrapScript("sh", "alpine"); got != "" {
		t.Errorf("BootstrapScript(sh, alpine) = %q, want empty", got)
	}
}

// The installed package name comes from the guest's runtime_packages table,
// not a name hardcoded per OS: arch calls its python3 package "python".
func TestBootstrapScriptInstallsGuestPackage(t *testing.T) {
	cases := []struct {
		os, wantInstall string
	}{
		{"alpine", "stoat_pkg_install python3"},
		{"arch", "stoat_pkg_install python"},
	}
	for _, c := range cases {
		got := BootstrapScript("python3", c.os)
		if !strings.Contains(got, "command -v python3") {
			t.Errorf("%s: missing the command -v check, got:\n%s", c.os, got)
		}
		if !strings.Contains(got, "stoat_pkg_setup") {
			t.Errorf("%s: missing stoat_pkg_setup, got:\n%s", c.os, got)
		}
		if !strings.Contains(got, c.wantInstall) {
			t.Errorf("%s: missing %q, got:\n%s", c.os, c.wantInstall, got)
		}
	}
}

func TestBootstrapScriptUnknownOSIsEmpty(t *testing.T) {
	if got := BootstrapScript("python3", "plan9"); got != "" {
		t.Errorf("BootstrapScript(python3, plan9) = %q, want empty for an unknown guest", got)
	}
}
