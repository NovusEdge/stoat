package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
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
	// v.Recipes only holds names the form offered for this VM's os/backend.
	// recipes.List already filters by backend at selection time, so every
	// entry here is already a cloud fragment. No extra backend check is
	// needed before reading them.
	var recipeBodies []string
	for _, name := range v.Recipes {
		body, err := recipes.Read(name)
		if err != nil {
			return fmt.Errorf("reading recipe %s: %w", name, err)
		}
		recipeBodies = append(recipeBodies, body)
	}
	if _, err := cloudinit.Seed(v, pub, recipeBodies); err != nil {
		return err
	}
	return nil
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
