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
