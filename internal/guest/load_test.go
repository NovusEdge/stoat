package guest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled files must parse, pass validation, and (via normalize)
// carry their own init system in capabilities.
func TestBundledFilesParseAndValidate(t *testing.T) {
	got := loadBundled()
	for _, name := range []string{"alpine", "ubuntu", "debian", "fedora", "arch", "almalinux", "rocky", "opensuse"} {
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

func TestEnterpriseGuestFacts(t *testing.T) {
	for _, tc := range []struct {
		name, capability, setup, runtime, install, hint string
		aliases                                         []string
	}{
		{name: "almalinux", capability: "dnf", runtime: "python3", install: "dnf install -y", hint: "almalinux", aliases: []string{"rpm-family"}},
		{name: "rocky", capability: "dnf", runtime: "python3", install: "dnf install -y", hint: "rocky", aliases: []string{"rpm-family"}},
		{name: "opensuse", capability: "zypper", setup: "zypper --non-interactive refresh", runtime: "python313", install: "zypper --non-interactive install", hint: "opensuse", aliases: []string{"rpm-family"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, ok := Lookup(tc.name)
			if !ok {
				t.Fatalf("guest %q is not bundled", tc.name)
			}
			if o.Init != InitSystemd || o.Shell != "/bin/bash" || o.DefaultBackend != "cloudinit" || o.DefaultSSHUser != "stoat" {
				t.Errorf("base facts = init=%q shell=%q backend=%q user=%q", o.Init, o.Shell, o.DefaultBackend, o.DefaultSSHUser)
			}
			if got, want := o.Escalate, []string{"sudo", "-n"}; !equalStrings(got, want) {
				t.Errorf("escalate = %v, want %v", got, want)
			}
			if !containsString(o.Capabilities, tc.capability) || !containsString(o.Capabilities, "systemd") {
				t.Errorf("capabilities = %v, want %q and systemd", o.Capabilities, tc.capability)
			}
			if !equalStrings(o.Aliases, tc.aliases) {
				t.Errorf("aliases = %v, want %v", o.Aliases, tc.aliases)
			}
			if !containsString(o.FilenameHints, tc.hint) {
				t.Errorf("filename hints = %v, want %q", o.FilenameHints, tc.hint)
			}
			if o.Pkg.Setup != tc.setup || strings.Join(o.Pkg.Install, " ") != tc.install || o.Pkg.RuntimePackages["python3"] != tc.runtime {
				t.Errorf("package facts = setup=%q install=%v runtime=%v", o.Pkg.Setup, o.Pkg.Install, o.Pkg.RuntimePackages)
			}
			if o.Pkg.ScaffoldInstall != tc.install+" " || o.Pkg.ScaffoldSetup != tc.setup {
				t.Errorf("scaffolds = setup=%q install=%q", o.Pkg.ScaffoldSetup, o.Pkg.ScaffoldInstall)
			}
			for action, want := range map[string]string{
				"enable": "systemctl enable {name}", "start": "systemctl start {name}",
				"stop": "systemctl stop {name}", "restart": "systemctl restart {name}", "status": "systemctl status {name}",
			} {
				if got := o.Svc.Get(action); got != want {
					t.Errorf("svc.%s = %q, want %q", action, got, want)
				}
			}
			if o.Cmd["download"] != "curl -fsSL -o" || o.Cmd["useradd"] != "useradd -m -s /bin/bash {name}" {
				t.Errorf("commands = %v", o.Cmd)
			}
			backend, ok := o.Backends["cloudinit"]["skip_9p"].(bool)
			if !ok || !backend {
				t.Errorf("cloudinit.skip_9p = %#v, want true", o.Backends["cloudinit"]["skip_9p"])
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

// A quote in a template survives the sh prelude but breaks the python one,
// where the template goes inside a single-quoted literal. Rejecting it at
// load turns a SyntaxError mid-recipe into a message about the file.
func TestValidateRejectsQuoteInTemplate(t *testing.T) {
	o := loadBundled()["fedora"]
	o.Svc.Enable = "systemctl enable 'x' {name}"
	err := validate(o)
	if err == nil || !strings.Contains(err.Error(), "svc.enable contains a single quote") {
		t.Errorf("err = %v", err)
	}
	o = loadBundled()["fedora"]
	o.Cmd = map[string]string{"reboot": "shutdown -r 'now'"}
	if err := validate(o); err == nil || !strings.Contains(err.Error(), "cmd.reboot contains a single quote") {
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
