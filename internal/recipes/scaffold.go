package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/guest"
)

// Dir is where recipes live. Exported so the TUI and CLI can point a user at
// it: authoring a recipe has always been "put a correctly named file here",
// and the only real problem with that was that nobody could tell.
func Dir() string { return dir() }

// shellTemplate is the skeleton `stoat recipe new` writes. It carries the two
// things every bundled shell recipe has and a first-timer would not think of:
// the repository enable (docker, tailscale and most of what you would want
// live in Alpine's community repo, which is off by default), and the
// live-vs-disk honesty block that the test suite REQUIRES of every bundled
// recipe: on a live VM the root is a tmpfs overlay, so anything installed is
// gone on reboot, and a recipe that implies otherwise is lying.
const shellTemplate = `#!/bin/sh
# %s: runs as root over ssh on a booted %s VM.
set -e

%s
# TODO: install what you actually want.
%s

# Live VMs are diskless: the root filesystem is a tmpfs/overlay in RAM, so
# everything installed above is gone on reboot. A disk install mounts a real
# block device as root, which persists. Detect it rather than assume.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
	echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot; rebooting will NOT bring it back. Use a disk VM to keep it."
	;;
*)
	echo "installed on a disk VM (root is $root_fstype): this survives a reboot."
	;;
esac
`

// cloudTemplate is the cloud-init equivalent. Cloud images are always a real
// disk install, so there is no live-vs-disk branch to be honest about.
const cloudTemplate = `#cloud-config
# %s: carried verbatim into the cloud-init seed's cloud-config-archive and
# applied at first boot; cloud-init merges it in, so any key it understands
# survives, not just packages:/runcmd:. Package names must be spelled the
# same on every distro this is offered to (apt and pacman for the shared
# fragment), or it needs to be a per-OS file: <name>.<os>.cloud.yaml.
packages:
  # TODO: packages, by their exact name on the target distro.

runcmd:
  # TODO: commands to run once, at first boot.
`

// osSetup is the package-manager preamble per OS, or "" where none is needed.
func osSetup(osName string) (setup, install string) {
	if os, ok := guest.Lookup(osName); ok {
		return os.PkgSetup, os.PkgInstall
	}
	return "", "# install: "
}

// New writes a skeleton recipe and returns its path. It refuses to overwrite:
// Install() already promises never to clobber a user's edits, and a scaffold
// command that could destroy the recipe you have been working on would be a
// worse failure than not existing.
func New(name, osName, backend string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("a recipe needs a name")
	}
	if strings.ContainsAny(name, "./ ") {
		return "", fmt.Errorf("recipe name %q cannot contain dots, slashes or spaces, since those separate the name from its os and extension", name)
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return "", err
	}

	var file, body string
	if backend == "cloudinit" {
		file = name + ".cloud.yaml"
		g, ok := guest.Lookup(osName)
		if osName != "" && (!ok || !g.CloudRecipes) {
			// An OS the shared fragment cannot serve needs its own file, for
			// the same reason Fedora has one: its package names differ.
			file = name + "." + osName + ".cloud.yaml"
		}
		body = fmt.Sprintf(cloudTemplate, name)
	} else {
		if osName == "" {
			return "", fmt.Errorf("a shell recipe needs an os (its filename is <name>.<os>.sh, and that is how the picker knows which VMs to offer it to)")
		}
		file = name + "." + osName + ".sh"
		setup, install := osSetup(osName)
		body = fmt.Sprintf(shellTemplate, name, osName, setup, install)
	}

	path := filepath.Join(dir(), file)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists: edit it instead", path)
	}

	mode := os.FileMode(0o644)
	if strings.HasSuffix(file, ".sh") {
		mode = 0o755
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return "", err
	}
	return path, nil
}

// Installed lists every recipe file in the data root, whether bundled or
// written by the user. Unlike List it does not filter by os or backend: this
// is "what is on disk", for a human looking for something to edit.
func Installed() ([]string, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		// Dotfiles are stoat's own bookkeeping (ManifestName), not
		// something anyone wants offered as a recipe to edit.
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}
