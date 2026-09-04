package guest

import (
	"strings"
	"testing"
)

// Every fact in the registry exists because a call site consumes it. These
// pin the ones whose absence is a SILENT failure rather than a visible one.
func TestRegistryFactsThatFailSilently(t *testing.T) {
	for _, tc := range []struct {
		os, shell string
		pkgs      []string
	}{
		// Alpine ships no bash. cloud-init's user module fails outright on a
		// missing shell: no account is created and no key lands. The only
		// symptom is "Permission denied (publickey)".
		{"alpine", "/bin/ash", []string{"sudo"}},
		{"ubuntu", "/bin/bash", nil},
		{"debian", "/bin/bash", nil},
		{"fedora", "/bin/bash", nil},
		{"arch", "/bin/bash", nil},
	} {
		got, ok := Lookup(tc.os)
		if !ok {
			t.Fatalf("%s is not in the registry", tc.os)
		}
		if got.Shell != tc.shell {
			t.Errorf("%s shell = %q, want %q", tc.os, got.Shell, tc.shell)
		}
		if len(got.SeedPackages) != len(tc.pkgs) {
			t.Errorf("%s SeedPackages = %v, want %v", tc.os, got.SeedPackages, tc.pkgs)
		}
	}
}

// Init must resolve to the OS's actual init system, not a Backend proxy.
// Alpine is the only OpenRC entry; that must not be the reason every other
// OS reports systemd.
func TestInitPerOS(t *testing.T) {
	for _, tc := range []struct {
		os   string
		init InitSystem
	}{
		{"alpine", InitOpenRC},
		{"ubuntu", InitSystemd},
		{"debian", InitSystemd},
		{"fedora", InitSystemd},
		{"arch", InitSystemd},
	} {
		got, ok := Lookup(tc.os)
		if !ok {
			t.Fatalf("%s is not in the registry", tc.os)
		}
		if got.Init != tc.init {
			t.Errorf("%s Init = %q, want %q", tc.os, got.Init, tc.init)
		}
	}
}

// An unknown OS must be reported as unknown, not silently defaulted by the
// registry. Callers decide what to do with that; the registry does not guess.
func TestLookupReportsUnknown(t *testing.T) {
	if _, ok := Lookup("plan9"); ok {
		t.Error("Lookup invented an OS")
	}
	if _, ok := Lookup(""); ok {
		t.Error("Lookup treated the empty OS as known")
	}
}

// Every declaration must be complete. A half-filled entry is exactly the
// silent-failure mode this package exists to prevent: an OS with no Shell
// would hand cloud-init an empty string.
func TestEveryDeclarationIsComplete(t *testing.T) {
	for _, o := range All() {
		if o.Name == "" {
			t.Error("an entry has no Name")
		}
		if o.Shell == "" {
			t.Errorf("%s has no Shell", o.Name)
		}
		if o.DefaultBackend == "" {
			t.Errorf("%s has no DefaultBackend", o.Name)
		}
		if o.Init == "" {
			t.Errorf("%s has no Init", o.Name)
		}
		if o.DefaultSSHUser == "" {
			t.Errorf("%s has no DefaultSSHUser", o.Name)
		}
		if o.Pkg.ScaffoldInstall == "" {
			t.Errorf("%s has no Pkg.ScaffoldInstall", o.Name)
		}
		if len(o.FilenameHints) == 0 {
			t.Errorf("%s has no FilenameHints, so a BYO image of it can never be recognised", o.Name)
		}
	}
}

// Hints must not collide: two OSes claiming the same substring makes BYO
// inference order-dependent, which is how a silent misidentification starts.
func TestFilenameHintsDoNotCollide(t *testing.T) {
	seen := map[string]string{}
	for _, o := range All() {
		for _, h := range o.FilenameHints {
			h = strings.ToLower(h)
			if other, dup := seen[h]; dup {
				t.Errorf("hint %q claimed by both %s and %s", h, other, o.Name)
			}
			seen[h] = o.Name
		}
	}
}
