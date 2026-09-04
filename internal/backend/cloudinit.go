package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/recipes"
)

// cloudinitBackend provisions a distro cloud image via a NoCloud seed ISO.
//
// The mode guard on Prepare and Args matters: a BYO Ubuntu ISO installed in
// disk mode still has Backend "cloudinit" from the registry, because it is
// still Ubuntu. It has no seed and must not get one. The seed was only ever
// built for v.Mode == "cloud"; the guard preserves that behaviour exactly.
type cloudinitBackend struct{}

func (cloudinitBackend) Name() string { return "cloudinit" }

// Prepare creates v's CoW overlay (backed by v.Base) and cloud-init seed the
// first time a cloud-mode VM starts. It no-ops on later starts and outside
// cloud mode.
//
// Once created, the overlay holds real guest state: installed packages,
// home directories. Prepare never rebuilds it the way apkovlBackend rebuilds
// its overlay on every start. See docs/design/guest-subsystem.md §10
// ("Risks") for why that asymmetry is intentional.
func (cloudinitBackend) Prepare(v *config.VM) error {
	if v.Mode != "cloud" {
		return nil
	}
	if _, err := os.Stat(v.DiskPath()); err == nil {
		return nil
	}
	base, err := filepath.Abs(v.Base)
	if err != nil {
		return err
	}
	out, err := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", v.DiskPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
	}
	// An overlay inherits its base image's virtual size. Cloud images are
	// sized to boot, not to install anything: Ubuntu 24.04's is 3.5G with a
	// 2.4G root. Installing a desktop fills the disk. apt then exits 100,
	// and cloud-init reports a bare "error" that looks like a broken recipe,
	// not a full disk.
	//
	// Resize here, before first boot, so cloud-init's own growpart/resizefs
	// expands the filesystem to match.
	if v.Disk != "" {
		out, err := exec.Command("qemu-img", "resize", v.DiskPath(), v.Disk).CombinedOutput()
		if err != nil {
			os.Remove(v.DiskPath())
			return fmt.Errorf("qemu-img resize to %s: %s", v.Disk, strings.TrimSpace(string(out)))
		}
	}
	if err := keys.Ensure(); err != nil {
		return err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return err
	}
	scripts, err := recipeScripts(v)
	if err != nil {
		return err
	}
	// WrapScripts renders every recipe into one write_files+runcmd fragment
	// cloud-init runs in order at first boot. An empty selection wraps to "",
	// which Seed must not carry as a document; pass no bodies instead.
	//
	// prelude is sh only: the cloud-init path has no runtime bootstrap step
	// (that is sshx.Provision's job), so a non-sh recipe here runs with a
	// bare body, same as before this feature.
	var prelude string
	if o, ok := guest.Lookup(v.OS); ok {
		prelude = guest.Prelude(o, "sh")
	}
	var bodies []string
	if frag := cloudinit.WrapScripts(scripts, prelude); frag != "" {
		bodies = []string{frag}
	}
	if _, err := cloudinit.Seed(v, pub, bodies); err != nil {
		return err
	}
	return nil
}

// recipeScripts resolves v.Recipes to the cloud-init scripts WrapScripts
// renders: each recipe's manifest, then the script body for v.OS. A recipe
// with no recipe.toml went missing since create time and errors here.
func recipeScripts(v *config.VM) ([]cloudinit.Script, error) {
	var scripts []cloudinit.Script
	for _, name := range v.Recipes {
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return nil, fmt.Errorf("reading recipe %s: %w", name, err)
		}
		if !ok {
			return nil, fmt.Errorf("reading recipe %s: no recipe.toml", name)
		}
		body, err := m.ScriptContent(v.OS)
		if err != nil {
			return nil, fmt.Errorf("reading recipe %s: %w", name, err)
		}
		scripts = append(scripts, cloudinit.Script{Name: name, Content: body})
	}
	return scripts, nil
}

// Args attaches the seed cdrom only in cloud mode; see the mode-guard note
// on cloudinitBackend.
func (cloudinitBackend) Args(v *config.VM) []string {
	if v.Mode != "cloud" {
		return nil
	}
	seedISO := filepath.Join(v.OvlDir(), "seed.iso")
	return []string{"-drive", "file=" + seedISO + ",media=cdrom"}
}
