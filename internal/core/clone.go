package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
)

// Clone duplicates VM name under newName. It is NOT a file copy — see
// docs/design/core-api.md §7/§7.1 item 9 and the per-field reasoning below —
// and every shortcut it takes is deliberate, not an oversight:
//
//   - The disk (when there is one) becomes a qcow2 OVERLAY backed by the
//     source's disk, not a byte copy. `qemu-img create -b` is close to
//     instant and the new file starts at a few KB, because it only ever
//     records blocks that diverge from the backing file.
//   - The SSH port is freshly allocated (config.FreePort), never inherited.
//   - Port forwards are DROPPED, never inherited.
//   - A cloud clone gets its own cloud-init identity — see cloneCloud.
//
// Refuses a RUNNING source. A qcow2 backing file must never change after an
// overlay is made from it (the overlay's unwritten blocks are read straight
// through to the backing file — if that file mutates, the overlay is reading
// stale-vs-live blocks at random, i.e. corrupt); a running source is a qemu
// process writing to its disk continuously, so there is no instant at which
// "the disk right now" is a safe backing file. Refusing is simpler and
// safer than racing to stop it or freezing writes some other way, and it
// matches Destroy's own precedent (ErrAlreadyRunning, "stop it first")
// rather than inventing a second shape for the same rule.
func Clone(name, newName string) (VM, error) {
	src, err := load(name)
	if err != nil {
		return VM{}, err
	}
	if qemu.Running(src) {
		return VM{}, fmt.Errorf("%w: %s: stop it before cloning -- a qcow2 overlay's backing file must not change after the overlay is made, and a running VM is writing to its disk continuously", ErrAlreadyRunning, name)
	}

	// Name validation duplicates plan()'s three rules (non-empty, no
	// slashes/spaces, not already taken) rather than sharing it: plan is
	// unexported in core.go, and the brief for this file is explicit that
	// core.go is off limits. Three lines of duplication was judged cheaper
	// than widening another file's edit surface to save them.
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

	// Field-by-field ruling on config.VM, worked out against the real
	// struct (internal/config/config.go) rather than assumed:
	//
	//   COPY (plain configuration, cloning it is the whole point):
	//     Mode, OS, ISO, RAM, CPUs, Disk (the size string), Installed
	//     (state of the disk content being cloned, not an identity), Share,
	//     Recipes, Backend, Base (the shared catalog image path -- already
	//     shared by every VM built from the same image, cloning changes
	//     nothing about that sharing), SSHUser, ConsolePassword (a VNC
	//     console password, not a network-facing secret or an identity --
	//     see cloudinit.consolePasswordBlock's own reasoning for why it is
	//     stored in the clear at all).
	//
	//   FRESH (identity-bearing; a copy here is the bug this function
	//   exists to prevent):
	//     SSHPort (config.FreePort, above). Forwards (dropped -- see the
	//     comment on the zero value below). Name (the parameter). Dir is
	//     recomputed by Save() itself. The cloud-init instance ID is not a
	//     config.VM field at all -- see cloneCloud.
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
		// Forwards is left at its zero value (nil), not copied. A forward is
		// a host TCP listener the CLONE would try to bind the moment it
		// starts -- if the source is (or later is) running with the same
		// forward, both VMs fight over one host port, exactly the collision
		// class validateForwards (forward.go) exists to prevent for SSHPort.
		// Silently keeping them would be an indefensible foot-gun (see this
		// package's Forward for how carefully that collision is guarded
		// elsewhere); dropping them and saying so here is the defensible
		// choice the task brief called for. A caller that wants the clone
		// reachable on the same extra ports calls Forward afterwards, on the
		// clone, and gets fresh validation against every other VM including
		// the source.
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
		// A live VM has no disk.qcow2 at all -- it boots off the ISO plus an
		// apkovl overlay that apkovlBackend.Prepare REBUILDS from vm.toml on
		// every single start (see apkovl.go's Prepare comment). There is
		// nothing here to clone: the clone's own first start builds its own
		// overlay from its own (copied) vm.toml fields, and produces the
		// same guest apkovlBackend.Prepare would have produced for the
		// source on its next boot anyway.
		return nil

	case "disk":
		// A real, independent qcow2 that Create allocated in full. Overlay
		// it: the clone gets whatever is currently on the source's disk
		// (installed OS, files, everything) for the cost of an empty file
		// that only grows as the clone's own writes diverge.
		return overlay(src.DiskPath(), clone.DiskPath())

	case "cloud":
		return cloneCloud(src, clone)

	default:
		// Not reached by anything plan() can produce today, but a silent
		// no-op here would leave a clone with neither a disk nor an error --
		// worse than failing loudly on a mode this function does not know
		// about yet.
		return fmt.Errorf("clone: unknown mode %q", src.Mode)
	}
}

// overlay creates dst as a qcow2 CoW overlay backed by src.
//
// -F (backing FORMAT, not just a bare -b path) is required, not optional,
// on the qemu-img installed in this environment: verified directly against
// qemu-img 11.0.2, which refuses `-b` without `-F` for a qcow2 backing file
// ("Backing file specified without backing format"). backend.Prepare
// (internal/backend/cloudinit.go) already learned this the same way; this
// mirrors it rather than risking a second, unverified guess.
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

// cloneCloud handles the one mode where "clone the disk" and "clone the
// cloud-init identity" are the same decision, not two.
//
// A cloud VM's disk.qcow2 is ALREADY an overlay on Base (see Create's own
// comment: it is deliberately not created at Create time, only on first
// start, by cloudinitBackend.Prepare). Two cases follow from that:
//
//  1. Source never started (disk.qcow2 does not exist yet): there is no
//     drift to preserve -- the source is, right now, indistinguishable from
//     a freshly-Created VM pointed at the same Base. clone.Base already
//     equals src.Base (copied above), so the CORRECT clone is to create
//     NOTHING here and let the clone's own first start build its own
//     overlay-on-Base and its own seed, exactly as if it had been Created
//     fresh. This is not a shortcut: doing anything else would mean
//     inventing state the source never had.
//
//  2. Source has started (disk.qcow2 exists, with real drift -- installed
//     packages, home directories, whatever the guest has done since first
//     boot): the clone overlays the SOURCE's disk (not Base), a chain of
//     two backing files, which qemu supports natively and is the only way
//     to preserve that drift cheaply. Chaining is judged acceptable here
//     because Base itself never changes after an image is downloaded, so
//     the chain's depth cannot grow past two.
//
//     This is the case the task brief's item 4 (cloud-init instance ID)
//     turns out to be entangled with item 1 (the disk), and it is the one
//     most likely to be silently wrong:
//     cloudinitBackend.Prepare (internal/backend/cloudinit.go) SKIPS both
//     the overlay-from-Base step AND the cloudinit.Seed call the instant
//     disk.qcow2 already exists ("if _, err := os.Stat(v.DiskPath()); err
//     == nil { return nil }"). If Clone pre-creates the clone's disk.qcow2
//     (which case 2 must, to preserve drift) and stops there, the clone's
//     seed.iso is NEVER written -- Prepare no-ops on first start because
//     the disk exists, Args still unconditionally attaches
//     "<ovl>/seed.iso" as a cdrom, and qemu fails at start with "Could not
//     open seed.iso" (the exact historical failure TestSeedErrorsWithoutXorriso
//     guards against for a different cause). This is a harder failure than
//     "cloud-init treats it as the same instance and skips first boot" --
//     it never gets that far.
//
//     So case 2 builds the seed itself, right here, doing the same three
//     steps Prepare would have (ensure the client keypair, read the
//     clone's own recipe bodies, call cloudinit.Seed) but against `clone`
//     -- whose Name is already the new name. cloudinit.Seed's meta-data
//     writes `instance-id: "stoat-" + v.Name` (see metaDataTemplate), so
//     seeding against clone rather than src is what gives the clone a
//     genuinely NEW cloud-init instance ID, not a copy of the source's.
//     That in turn is what makes cloud-init treat the clone as a new
//     instance at boot and run its per-instance modules (user creation,
//     authorized_keys) again instead of finding a matching instance-id
//     already recorded on the overlaid disk and skipping first boot
//     entirely -- the exact failure the design doc's item 4 warns about.
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
	// disk.qcow2 exists -- see the function comment above.
	if err := keys.Ensure(); err != nil {
		return err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return err
	}
	var bodies []string
	for _, name := range clone.Recipes {
		body, err := recipes.Read(name)
		if err != nil {
			return fmt.Errorf("clone: reading recipe %s: %w", name, err)
		}
		bodies = append(bodies, body)
	}
	_, err = cloudinit.Seed(clone, pub, bodies)
	return err
}
