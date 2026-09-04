package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The five bundled files must parse, pass validation, and (via normalize)
// carry their own init system in capabilities.
func TestBundledFilesParseAndValidate(t *testing.T) {
	got := loadBundled()
	for _, name := range []string{"alpine", "ubuntu", "debian", "fedora", "arch"} {
		o, ok := got[name]
		if !ok {
			t.Fatalf("no bundled %s", name)
		}
		if o.Source != "bundled" {
			t.Errorf("%s source = %q, want bundled", name, o.Source)
		}
		if err := validate(o); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	alpine := got["alpine"]
	last := alpine.Capabilities[len(alpine.Capabilities)-1]
	if last != "openrc" {
		t.Errorf("alpine capabilities = %v, want openrc last", alpine.Capabilities)
	}
}

func TestValidateMissingField(t *testing.T) {
	o := loadBundled()["fedora"]
	o.Pkg.Install = nil
	err := validate(o)
	if err == nil || !strings.Contains(err.Error(), "fedora: missing pkg.install") {
		t.Errorf("err = %v", err)
	}
}

func TestValidateRejectsForeignInit(t *testing.T) {
	o := loadBundled()["alpine"]
	o.Capabilities = append(o.Capabilities, "systemd")
	err := validate(o)
	if err == nil || !strings.Contains(err.Error(), `capabilities lists init "systemd" but init is "openrc"`) {
		t.Errorf("err = %v", err)
	}
}

func TestParseFileRejectsUnknownKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.toml")
	body := "schema = 1\nname = \"x\"\n[pkg]\ninstal = [\"apk\"]\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFile(p)
	if err == nil || !strings.Contains(err.Error(), `unknown key "pkg.instal"`) {
		t.Errorf("err = %v", err)
	}
}

func TestParseFileRequiresSchema(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.toml")
	if err := os.WriteFile(p, []byte("name = \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := parseFile(p)
	if err == nil || !strings.Contains(err.Error(), "missing schema") {
		t.Errorf("err = %v", err)
	}
}

// A user file naming a bundled guest merges per field: a scalar it sets
// wins, a table it omits (pkg) keeps the bundled value, and a backend
// table it does set replaces whole rather than merging key by key.
func TestLoadMergesUserOverBundled(t *testing.T) {
	dir := t.TempDir()
	body := "schema = 1\nname = \"fedora\"\nshell = \"/bin/zsh\"\n[backend.cloudinit]\nskip_9p = true\n"
	if err := os.WriteFile(filepath.Join(dir, "fedora.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { loaded = loadBundled() })
	if err := Load(dir); err != nil {
		t.Fatal(err)
	}
	o, ok := Lookup("fedora")
	if !ok {
		t.Fatal("fedora missing after Load")
	}
	if o.Shell != "/bin/zsh" || o.Source != "bundled+user" {
		t.Errorf("shell = %q source = %q", o.Shell, o.Source)
	}
	if len(o.Pkg.Install) == 0 || o.Pkg.Install[0] != "dnf" {
		t.Errorf("merge lost pkg.install: %v", o.Pkg.Install)
	}
	if o.Backends["cloudinit"]["skip_9p"] != true {
		t.Errorf("backend table not replaced: %v", o.Backends)
	}
}

// A file naming a guest not already loaded is a new guest, not a merge: it
// must carry every required field on its own.
func TestLoadNewGuestNeedsEveryField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "freebsd.toml"), []byte("schema = 1\nname = \"freebsd\"\ninit = \"rc\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { loaded = loadBundled() })
	err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "freebsd: missing shell") {
		t.Errorf("err = %v", err)
	}
	if _, ok := Lookup("freebsd"); ok {
		t.Error("a failed Load changed the loaded set")
	}
}

// Load runs at startup with a directory the user may never have created.
func TestLoadMissingDirIsFine(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Fatal(err)
	}
}

// Capabilities replaces recipes/manifest.go's capabilityOSes table: each
// capability, including the init name the loader appends, must map back
// to the guests that declare it.
func TestCapabilitiesIncludeInit(t *testing.T) {
	c := Capabilities()
	if got := c["openrc"]; len(got) != 1 || got[0] != "alpine" {
		t.Errorf("openrc = %v", got)
	}
	if got := c["apt"]; len(got) != 2 {
		t.Errorf("apt = %v", got)
	}
}
