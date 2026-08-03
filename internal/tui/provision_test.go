package tui

import (
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestInstallerName: a known OS with an Installer in the registry gets named
// exactly; an unknown OS or one with no Installer stays generic — naming the
// wrong installer is worse than staying general.
func TestInstallerName(t *testing.T) {
	cases := []struct {
		os, want string
	}{
		{"alpine", "setup-alpine"},
		{"ubuntu", "the installer"},
		{"nonexistent-os", "the installer"},
		{"", "the installer"},
	}
	for _, c := range cases {
		got := installerName(&config.VM{OS: c.os})
		if got != c.want {
			t.Errorf("installerName(OS=%q) = %q, want %q", c.os, got, c.want)
		}
	}
}
