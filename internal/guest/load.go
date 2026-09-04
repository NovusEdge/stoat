package guest

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/tomlx"
)

//go:embed bundled/*.toml
var bundledFS embed.FS

// schemaVersion is the guest.toml schema this stoat reads.
const schemaVersion = 1

var initNames = map[InitSystem]bool{InitSystemd: true, InitOpenRC: true, InitRC: true}

// parseFile reads one guest.toml. Unknown keys are an error: the file is
// hand-written and a typo must not become a silent default.
func parseFile(path string) (OS, error) {
	var o OS
	if err := tomlx.Decode(path, &o, tomlx.Reject, tomlx.Schema(schemaVersion)); err != nil {
		return OS{}, err
	}
	if o.Schema == 0 {
		return OS{}, fmt.Errorf("%s: missing schema", path)
	}
	return o, nil
}

// loadBundled parses every embedded file. A bundled file that fails is a
// build error, so it panics.
func loadBundled() map[string]OS {
	out := map[string]OS{}
	entries, err := fs.ReadDir(bundledFS, "bundled")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		tmp, err := os.CreateTemp("", "stoat-guest-*.toml")
		if err != nil {
			panic(err)
		}
		b, _ := bundledFS.ReadFile("bundled/" + e.Name())
		if _, err := tmp.Write(b); err != nil {
			panic(err)
		}
		tmp.Close()
		o, err := parseFile(tmp.Name())
		os.Remove(tmp.Name())
		if err != nil {
			panic(fmt.Sprintf("bundled guest %s: %v", e.Name(), err))
		}
		if err := validate(o); err != nil {
			panic(fmt.Sprintf("bundled guest %s: %v", e.Name(), err))
		}
		o.Source = "bundled"
		out[o.Name] = normalize(o)
	}
	return out
}

// validate reports the first missing required field or inconsistency.
func validate(o OS) error {
	req := []struct {
		name string
		ok   bool
	}{
		{"name", o.Name != ""},
		{"init", o.Init != ""},
		{"shell", o.Shell != ""},
		{"default_backend", o.DefaultBackend != ""},
		{"default_ssh_user", o.DefaultSSHUser != ""},
		{"escalate", o.Escalate != nil},
		{"capabilities", o.Capabilities != nil},
		{"seed_packages", o.SeedPackages != nil},
		{"pkg.install", len(o.Pkg.Install) > 0},
		{"pkg.scaffold_install", o.Pkg.ScaffoldInstall != ""},
		{"pkg.runtime_packages", o.Pkg.RuntimePackages != nil},
		{"svc.enable", o.Svc.Enable != ""},
		{"svc.start", o.Svc.Start != ""},
		{"svc.stop", o.Svc.Stop != ""},
		{"svc.restart", o.Svc.Restart != ""},
		{"svc.status", o.Svc.Status != ""},
	}
	for _, r := range req {
		if !r.ok {
			return fmt.Errorf("guest.toml: %s: missing %s", o.Name, r.name)
		}
	}
	if !initNames[o.Init] {
		return fmt.Errorf("guest.toml: %s: init %q is not systemd, openrc, or rc", o.Name, o.Init)
	}
	for _, c := range o.Capabilities {
		if initNames[InitSystem(c)] && InitSystem(c) != o.Init {
			return fmt.Errorf("guest.toml: %s: capabilities lists init %q but init is %q", o.Name, c, o.Init)
		}
	}
	return nil
}

// normalize appends init to capabilities once and fills nil maps, so a
// consumer never branches on nil.
func normalize(o OS) OS {
	has := false
	for _, c := range o.Capabilities {
		if c == string(o.Init) {
			has = true
		}
	}
	if !has {
		o.Capabilities = append(append([]string{}, o.Capabilities...), string(o.Init))
	}
	if o.Cmd == nil {
		o.Cmd = map[string]string{}
	}
	if o.Backends == nil {
		o.Backends = map[string]map[string]any{}
	}
	if o.Pkg.Env == nil {
		o.Pkg.Env = map[string]string{}
	}
	return o
}

// sortedNames returns the keys of m in order, for a stable All().
func sortedNames(m map[string]OS) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var loaded = loadBundled()

func Load(dir string) error { return fmt.Errorf("guest: Load not implemented") }

func Capabilities() map[string][]string { return nil }

// userFiles lists <dir>/*.toml, or nothing when dir is absent.
func userFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}
