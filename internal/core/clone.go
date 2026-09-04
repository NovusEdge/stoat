package core

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
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
)

// Clone duplicates VM name under newName. It is not a file copy (design §7):
//
//   - The disk, when there is one, becomes a qcow2 overlay backed by the
//     source's disk. qemu-img create -b is near-instant and records only
//     blocks that diverge from the backing file.
//   - The SSH port is freshly allocated (config.FreePort).
//   - Port forwards are dropped.
//   - A cloud clone gets its own cloud-init identity; see cloneCloud.
//
// Refuses a running source. An overlay reads unwritten blocks straight through
// to its backing file, so the backing file must not change after the overlay
// is made; a running qemu writes to its disk continuously. Matches Destroy's
// ErrAlreadyRunning precedent.
func Clone(name, newName string) (VM, error) {
	// Held for the whole operation. FreePort and the name-taken check are
	// allocate-now-commit-later, so concurrent callers collide. And the
	// overlay references the source's disk by path: without the lock a
	// concurrent Destroy of the stopped source removes that disk between the
	// running check and the qemu-img call, and the clone fails only at first
	// start with "Could not open backing file". Destroy takes the lock too.
	unlock, err := config.Lock()
	if err != nil {
		return VM{}, err
	}
	defer unlock()

	src, err := load(name)
	if err != nil {
		return VM{}, err
	}
	if qemu.Running(src) {
		return VM{}, fmt.Errorf("%w: %s: stop it before cloning", ErrAlreadyRunning, name)
	}

	// Duplicates plan()'s three name rules rather than sharing the unexported
	// helper in core.go.
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return VM{}, fmt.Errorf("%w: name is required", ErrInvalidSpec)
	}
	if strings.ContainsAny(newName, "/ ") {
		return VM{}, fmt.Errorf("%w: name cannot contain spaces or slashes", ErrInvalidSpec)
	}
	if _, err := os.Stat(filepath.Join(config.Root(), newName)); err == nil {
		return VM{}, fmt.Errorf("%w: %s", ErrNameTaken, newName)
	}

	port, err := config.FreePort()
	if err != nil {
		return VM{}, err
	}

	// config.VM splits into two groups. Copy the configuration: Mode, OS, ISO,
	// RAM, CPUs, Disk, Installed, Share, Recipes, Backend, Base, SSHUser,
	// ConsolePassword. Make the identity-bearing fields fresh: SSHPort
	// (FreePort), Forwards (dropped), Name (the parameter). Save recomputes
	// Dir. The cloud-init instance ID is not a config.VM field; see cloneCloud.
	clone := &config.VM{
		Name:      newName,
		Mode:      src.Mode,
		OS:        src.OS,
		ISO:       src.ISO,
		RAM:       src.RAM,
		CPUs:      src.CPUs,
		Disk:      src.Disk,
		Installed: src.Installed,
		Share:     src.Share,
		SSHPort:   port,
		// Forwards is nil, not copied. A forward is a host TCP listener the
		// clone binds at start; keeping the source's forwards makes both VMs
		// fight over one host port, the collision validateForwards prevents.
		// A caller wanting the same ports calls Forward on the clone, which
		// revalidates against every VM including the source.
		Recipes:         append([]string(nil), src.Recipes...),
		Backend:         src.Backend,
		Base:            src.Base,
		SSHUser:         src.SSHUser,
		ConsolePassword: src.ConsolePassword,
	}

	if err := clone.Save(); err != nil {
		return VM{}, err
	}

	if err := cloneDisk(src, clone); err != nil {
		// Leave no trace of a failed clone, matching Create's own rule for a
		// failed qemu-img call: otherwise List shows a VM with no disk that
		// can never boot.
		os.RemoveAll(clone.Dir)
		return VM{}, err
	}

	return fromConfig(clone), nil
}

// cloneDisk gives clone whatever disk state src's mode implies. Dispatches
// on Mode, not Backend, matching Args (internal/qemu/args.go): Mode owns the
// boot media, Backend owns provisioning, and disk handling here is squarely
// about the former.
func cloneDisk(src, clone *config.VM) error {
	switch src.Mode {
	case "live":
		// A live VM has no disk.qcow2: apkovlBackend.Prepare rebuilds its
		// overlay from vm.toml on every start. The clone's first start builds
		// its own from the copied fields.
		return nil

	case "disk":
		// A full qcow2 Create allocated. Overlay it so the clone gets the
		// source's current disk content for the cost of an empty file.
		return overlay(src.DiskPath(), clone.DiskPath())

	case "cloud":
		return cloneCloud(src, clone)

	default:
		// plan() produces nothing else today. Fail loudly rather than leave a
		// clone with neither a disk nor an error.
		return fmt.Errorf("clone: unknown mode %q", src.Mode)
	}
}

// overlay creates dst as a qcow2 CoW overlay backed by src.
//
// -F (backing format) is required alongside -b: qemu-img 11.0.2 refuses a
// qcow2 backing file without it ("Backing file specified without backing
// format"). backend.Prepare passes it for the same reason.
func overlay(src, dst string) error {
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("clone: source disk %s: %w", abs, err)
	}
	out, err := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", abs, "-F", "qcow2", dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// cloneCloud clones a cloud VM's disk and cloud-init identity together.
//
// A cloud VM's disk.qcow2 is an overlay on Base, written by
// cloudinitBackend.Prepare on first start, not at Create time.
//
//  1. Source never started (no disk.qcow2): there is no drift to preserve.
//     clone.Base already equals src.Base, so this creates nothing; the
//     clone's own first start builds its overlay and seed.
//
//  2. Source has started (disk.qcow2 exists, with drift): the clone
//     overlays the source's disk, a two-deep backing chain. Base never
//     changes after download, so the chain cannot grow past two.
//
//     Prepare no-ops once disk.qcow2 exists, so a pre-created clone disk
//     would never get its seed.iso written, and Args attaches seed.iso
//     unconditionally: qemu then fails at start with "Could not open
//     seed.iso". So case 2 seeds here against `clone` (keypair, recipe
//     bodies, cloudinit.Seed). Seed writes instance-id "stoat-"+Name, so
//     the clone gets a new cloud-init instance ID and reruns first-boot
//     modules instead of matching the source's recorded ID.
func cloneCloud(src, clone *config.VM) error {
	if _, err := os.Stat(src.DiskPath()); err != nil {
		// Case 1: nothing to do. clone.Base already points at the same
		// image; the clone's own first start does the rest.
		return nil
	}

	// Case 2: overlay the source's disk (not Base) to keep its drift.
	if err := overlay(src.DiskPath(), clone.DiskPath()); err != nil {
		return err
	}

	// Rebuild the seed against `clone`, not `src`, so its instance-id is
	// new. Mirrors cloudinitBackend.Prepare's own steps (keys.Ensure,
	// keys.PublicKey, read each recipe body, cloudinit.Seed) because
	// Prepare itself will no-op on the clone's first start now that
	// disk.qcow2 exists; see the function comment above.
	if err := keys.Ensure(); err != nil {
		return err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return err
	}
	var scripts []cloudinit.Script
	for _, name := range clone.Recipes {
		m, ok, err := recipes.ManifestFor(name)
		if err != nil {
			return fmt.Errorf("clone: reading recipe %s: %w", name, err)
		}
		if !ok {
			return fmt.Errorf("clone: reading recipe %s: no recipe.toml", name)
		}
		body, err := m.ScriptContent(clone.OS)
		if err != nil {
			return fmt.Errorf("clone: reading recipe %s: %w", name, err)
		}
		scripts = append(scripts, cloudinit.Script{Name: name, Content: body})
	}
	var prelude string
	if o, ok := guest.Lookup(clone.OS); ok {
		prelude = guest.Prelude(o, "sh")
	}
	var bodies []string
	if frag := cloudinit.WrapScripts(scripts, prelude); frag != "" {
		bodies = []string{frag}
	}
	_, err = cloudinit.Seed(clone, pub, bodies)
	return err
}
