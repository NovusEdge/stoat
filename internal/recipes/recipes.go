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
// guest.OS.CloudRecipes and devtools.cloud.yaml vs xfce.cloud.yaml). Fedora
// is kept out of the shared set entirely rather than being added to it, for
// two reasons that matter if this ever grows past a single bool:
//
//  1. The switch below resolves a shared fragment and a per-OS fragment
//     separately: a name with BOTH (xfce does) would offer Fedora two
//     entries for the same recipe the instant CloudRecipes were true for it
//     — the shared one (now wrongly in scope) and the per-OS one. Nothing
//     here makes the per-OS file suppress the shared one; that only holds
//     today because Fedora's CloudRecipes is false.
//  2. It would also silently break any shared-only fragment (no per-OS
//     override) whose package names happen not to hold on dnf —
//     devtools.cloud.yaml is one: git/curl/ca-certificates/tmux/less match,
//     but Fedora's vim package is "vim-enhanced", not "vim".
func List(osName, backend string) ([]string, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case backend == "cloudinit" && strings.HasSuffix(name, ".cloud.yaml"):
			// "xfce.fedora.cloud.yaml" -> per-OS, offered only to fedora.
			// "xfce.cloud.yaml"        -> shared, offered to CloudRecipes
			// OSes.
			// A per-OS fragment and the shared one therefore never both
			// appear for the same OS, so the picker shows one xfce entry.
			base := strings.TrimSuffix(name, ".cloud.yaml")
			if i := strings.LastIndex(base, "."); i >= 0 {
				if base[i+1:] == osName {
					out = append(out, name)
				}
			} else if g, ok := guest.Lookup(osName); ok && g.CloudRecipes {
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
