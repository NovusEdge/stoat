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

// cloudOS is the set of os values the single shared cloud-config fragment
// (xfce.cloud.yaml) supports. cloud-init's packages: list has no per-distro
// syntax, so one fragment only works across OSes whose package names happen
// to match — see that file's header comment for why Fedora is excluded.
var cloudOS = map[string]bool{"ubuntu": true, "debian": true, "arch": true}

// List returns installed recipe names offered for osName on backend.
//
// Shell recipes are named "xfce.<os>.sh" and only make sense pushed
// interactively over a live shell, so they're offered on the apkovl/ssh
// backends, filtered to the exact matching OS. The cloud-config fragment
// ("xfce.cloud.yaml") only makes sense merged into a cloud-init seed, so
// it's offered only on the cloudinit backend, filtered to cloudOS. A
// recipe with no file for the (osName, backend) combination is not
// offered — e.g. List("ubuntu", "cloudinit") never returns xfce.ubuntu.sh,
// and List("alpine", "apkovl") never returns the cloud fragment.
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
			if cloudOS[osName] {
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
