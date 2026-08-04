package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/guest"
)

// Dir is where recipes live. Exported so the TUI and CLI can point a user at
// it: authoring a recipe has always been "put a correctly named directory here
// with a recipe.toml", and the only real problem was that nobody could tell.
func Dir() string { return dir() }

// manifestTemplate is the recipe.toml skeleton `stoat recipe new` writes.
const manifestTemplate = `name = "%s"
description = "TODO: describe what this recipe does"
os = ["%s"]
stage = "provision"
script = "install.sh"
`

// shellTemplate is the install.sh skeleton. It carries the two things every
// bundled shell recipe has and a first-timer would not think of: the
// repository enable (docker, tailscale and most of what you would want live
// in Alpine's community repo, which is off by default), and the live-vs-disk
// honesty block that the test suite REQUIRES of every bundled recipe: on a
// live VM the root is a tmpfs overlay, so anything installed is gone on
// reboot, and a recipe that implies otherwise is lying.
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

// osSetup is the package-manager preamble per OS, or "" where none is needed.
func osSetup(osName string) (setup, install string) {
	if os, ok := guest.Lookup(osName); ok {
		return os.PkgSetup, os.PkgInstall
	}
	return "", "# install: "
}

// New writes a skeleton recipe directory and returns its path. It refuses to
// overwrite: Install() already promises never to clobber a user's edits, and
// a scaffold command that could destroy the recipe you have been working on
// would be a worse failure than not existing.
func New(name, osName, _ string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("a recipe needs a name")
	}
	if strings.ContainsAny(name, "./ ") {
		return "", fmt.Errorf("recipe name %q cannot contain dots, slashes or spaces", name)
	}
	if osName == "" {
		return "", fmt.Errorf("a recipe needs an os to target")
	}
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return "", err
	}

	recipeDir := filepath.Join(dir(), name)
	if _, err := os.Stat(recipeDir); err == nil {
		return "", fmt.Errorf("%s already exists: edit it instead", recipeDir)
	}

	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		return "", err
	}

	// Write recipe.toml
	manifest := fmt.Sprintf(manifestTemplate, name, osName)
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		return "", err
	}

	// Write install.sh
	setup, install := osSetup(osName)
	script := fmt.Sprintf(shellTemplate, name, osName, setup, install)
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte(script), 0o755); err != nil {
		return "", err
	}

	return recipeDir, nil
}

// Installed lists every recipe in the data root, whether bundled or written
// by the user. Unlike List it does not filter by os: this is "what is on
// disk", for a human looking for something to edit.
func Installed() ([]string, error) {
	manifests, err := ListManifests()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(manifests))
	for i, m := range manifests {
		out[i] = m.Name
	}
	return out, nil
}
