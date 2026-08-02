// Package recipes manages the shell/cloud-config scripts stoat runs inside
// guests.
package recipes

import (
	"embed"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

//go:embed *.sh *.yaml
var bundled embed.FS

func dir() string { return filepath.Join(config.Root(), "recipes") }

// Path is the on-disk location of a recipe. name is the full filename
// (including its .sh or .yaml extension) as returned by List.
func Path(name string) string { return filepath.Join(dir(), name) }

// Install copies bundled recipes into the data root. Existing files are never
// overwritten, so local edits survive upgrades.
func Install() error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	items, err := bundled.ReadDir(".")
	if err != nil {
		return err
	}
	for _, it := range items {
		dst := filepath.Join(dir(), it.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		b, err := bundled.ReadFile(it.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// List returns installed recipe names offered for osName on backend.
//
// Shell recipes are named "xfce.<os>.sh" and only make sense pushed
// interactively over a live shell, so they're offered on the apkovl/ssh
// backends, filtered to the exact matching OS. The cloud-config fragment
// ("xfce.cloud.yaml") only makes sense merged into a cloud-init seed, so
// it's offered only on the cloudinit backend, filtered to
// guest.Lookup(osName).CloudRecipes — unless a per-OS fragment
// ("xfce.fedora.cloud.yaml") exists for that OS. A recipe with no file for
// the (osName, backend) combination is not offered — e.g.
// List("ubuntu", "cloudinit") never returns xfce.ubuntu.sh, and
// List("alpine", "apkovl") never returns the cloud fragment.
//
// CloudRecipes is a single bool per OS, but "the shared fragment set" is
// really cloud-init's packages: list having no per-distro syntax: one
// fragment only works across OSes whose package names happen to match, so an
// OS can be safe for one shared fragment and not another (see
// guest.OS.CloudRecipes and devtools.cloud.yaml vs xfce.cloud.yaml). Alpine
// is CloudRecipes-eligible (devtools.cloud.yaml installs fine on apk) but
// still needs its own xfce.alpine.cloud.yaml, because xfce.cloud.yaml's
// systemctl runcmd does not run under Alpine's OpenRC — so, unlike Fedora
// (kept out of the shared set entirely), a per-OS override and CloudRecipes
// being true for the same OS both happen at once here. The first loop below
// records which base names have a per-OS override for osName so the second
// loop can suppress the shared fragment for exactly those names: without
// it, an OS with both would be offered two entries for the same recipe —
// the shared one (wrongly in scope) and the per-OS one.
//
// Fedora is kept out of the shared set entirely for a second, independent
// reason that still matters even with per-OS suppression: it would silently
// break any shared-only fragment (no per-OS override) whose package names
// happen not to hold on dnf — devtools.cloud.yaml is one:
// git/curl/ca-certificates/tmux/less match, but Fedora's vim package is
// "vim-enhanced", not "vim".
func List(osName, backend string) ([]string, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// overridden collects the base name of every per-OS fragment that
	// matches osName ("xfce.alpine.cloud.yaml" -> "xfce"), so the shared
	// fragment of the same name is suppressed below rather than offered
	// alongside it.
	overridden := map[string]bool{}
	if backend == "cloudinit" {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".cloud.yaml") {
				continue
			}
			base := strings.TrimSuffix(name, ".cloud.yaml")
			i := strings.LastIndex(base, ".")
			if i >= 0 && base[i+1:] == osName {
				overridden[base[:i]] = true
			}
		}
	}

	var out []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case backend == "cloudinit" && strings.HasSuffix(name, ".cloud.yaml"):
			// "xfce.fedora.cloud.yaml" -> per-OS, offered only to fedora.
			// "xfce.cloud.yaml"        -> shared, offered to CloudRecipes
			// OSes that don't have their own override for "xfce".
			base := strings.TrimSuffix(name, ".cloud.yaml")
			if i := strings.LastIndex(base, "."); i >= 0 {
				if base[i+1:] == osName {
					out = append(out, name)
				}
			} else if g, ok := guest.Lookup(osName); ok && g.CloudRecipes && !overridden[base] {
				out = append(out, name)
			}
		case backend != "cloudinit" && strings.HasSuffix(name, ".sh"):
			// "xfce.alpine.sh" -> "alpine"
			fields := strings.Split(strings.TrimSuffix(name, ".sh"), ".")
			fileOS := fields[len(fields)-1]
			if fileOS == osName {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Read returns a recipe's body. name is the full filename as returned by
// List.
func Read(name string) (string, error) {
	b, err := os.ReadFile(Path(name))
	return string(b), err
}
