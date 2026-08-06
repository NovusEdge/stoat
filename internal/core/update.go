package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// ErrImmutableField is returned by Update when a Patch tries to change a
// field that has no meaningful "after the fact" value; see Patch's own
// comment for which fields those are and why. Named, not silent: a caller
// that thinks it renamed or re-OSed a VM must be told it didn't, not have
// the request quietly partially applied.
var ErrImmutableField = errors.New("immutable field")

// ErrDiskShrink is returned by Update when Patch.Disk names a size smaller
// than the VM's current one. Shrinking a qcow2 truncates it and corrupts
// whatever filesystem is inside: qemu-img will do it anyway if asked;
// Update won't. Matches the create path's ParseSize, which refuses a
// relative size ("+8G") for the adjacent reason: both exist because
// `qemu-img resize` reads a leading "+" as "grow by", which is how an 8G
// disk silently became 16G before ParseSize existed.
var ErrDiskShrink = errors.New("disk can only grow")

// Patch describes an edit to an existing VM. Every field is a pointer so
// "not set" (nil, leave alone) is distinguishable from "set to the zero
// value" (e.g. an explicit Share: "" to clear a share), matching the design
// doc's requirement (docs/design/core-api.md §2.1) that a Patch be able to
// state that difference.
//
// Which fields are here, and what setting each one does, follows directly
// from §2.1 and the mutability table in §8 (decision 5), the same rules
// internal/tui/edit.go's validate already enforces for the TUI, which this
// must not be looser than:
//
//   - Name, OS, Backend, Mode: IMMUTABLE. Included here (rather than simply
//     not existing on the struct) so a caller that round-trips a full VM
//     through Patch gets ErrImmutableField naming the field it should not
//     have touched, instead of a Go compile error for a caller building a
//     Patch by hand, or a silently-dropped field for a caller building one
//     from a generic map (an MCP tool's JSON args, notably). Mode is
//     documented in the design doc as immutable only "after first boot";
//     see the note on checkImmutable for why this implementation treats it
//     as unconditionally immutable instead, which is a real narrowing of
//     the design doc reported to the brief's author rather than silently
//     decided here.
//   - RAM, CPUs, Share, SSHPort: safe, applies at the VM's next start.
//   - Recipes: safe, applies immediately (it only affects the NEXT Apply
//     call; nothing reads it while a VM is mid-boot).
//   - Disk: constrained. Grow-only, absolute size only, and, per §8's
//     mutability table, which classes a disk grow as Destructive, refused
//     outright while the VM is running rather than merely deferred.
type Patch struct {
	Name    *string
	OS      *string
	Backend *string
	Mode    *string

	RAM     *int
	CPUs    *int
	Share   *string
	SSHPort *int
	Disk    *string

	// Recipes replaces the recipe list wholesale when non-nil, including
	// with an empty (but non-nil) slice to clear it. A nil Recipes leaves
	// the list untouched, matching every other field's "nil means don't
	// touch" rule; there is no other way to tell "clear the list" and
	// "didn't mention it" apart with a bare []string.
	Recipes *[]string
}

// checkImmutable reports ErrImmutableField, naming the field, for any of
// Name/OS/Backend/Mode the Patch sets to something other than the VM's
// current value. Setting one to its CURRENT value is not an error: a
// caller that read a VM and Patches it back with every field populated
// (the natural shape for an MCP tool taking a full object) must not be
// punished for fields it never meant to change.
//
// Mode is held immutable unconditionally, not just "after first boot" as
// §2.1 states. The reason is not a shortcut: nothing in the codebase can
// answer "has this VM booted before" for all three modes today. Installed
// (config.VM's own field) only means anything for disk mode, where it
// tracks boot ORDER, not "ever booted" (internal/qemu/run.go's diskWritten,
// a >10MB disk.qcow2, sets it, and a disk-mode VM's qcow2 is created at
// full size by Create, so the flag can go true from Create alone if
// something merely fills the file, without qemu ever running). A cloud VM's
// disk.qcow2 is lazily created on first start (Create's own comment
// explains why), so its existence is a real first-boot signal for cloud,
// but only for cloud. A live VM persists nothing at all: its apkovl overlay
// is rebuilt from scratch on every single start
// (internal/backend/apkovl.go), so there is no on-disk artifact whose
// presence means "this live VM has booted." Three incompatible,
// mode-specific proxies (one of which doesn't exist) are not "a first-boot
// signal": inventing a fourth, new, persisted flag JUST for Update to key
// on was judged out of scope for a two-file change that must not touch
// config.VM. This is a real narrowing of §2.1, reported rather than
// silently done: Mode is immutable, full stop, until a genuine
// mode-agnostic "has started" signal exists for Update to use.
func checkImmutable(v *config.VM, p Patch) error {
	if p.Name != nil && *p.Name != v.Name {
		return fmt.Errorf("%w: name", ErrImmutableField)
	}
	if p.OS != nil && *p.OS != v.OS {
		return fmt.Errorf("%w: os", ErrImmutableField)
	}
	if p.Backend != nil && *p.Backend != v.Backend {
		return fmt.Errorf("%w: backend", ErrImmutableField)
	}
	if p.Mode != nil && *p.Mode != v.Mode {
		return fmt.Errorf("%w: mode", ErrImmutableField)
	}
	return nil
}

// Update applies p to VM name and returns the resulting view.
//
// Held under config.Lock() across the whole read-modify-write, matching
// Create/Clone/Destroy/Prune: SSHPort's collision check reads every other
// VM's claimed ports the same way FreePort and validateForwards do, and
// without the lock, two concurrent Updates (or an Update racing a Create)
// can both see the same port as free and both claim it.
func Update(name string, p Patch) (VM, error) {
	unlock, err := config.Lock()
	if err != nil {
		return VM{}, err
	}
	defer unlock()

	v, err := load(name)
	if err != nil {
		return VM{}, err
	}

	if err := checkImmutable(v, p); err != nil {
		return VM{}, err
	}

	if p.RAM != nil {
		if *p.RAM < 256 {
			return VM{}, fmt.Errorf("%w: ram must be at least 256 MB", ErrInvalidSpec)
		}
		v.RAM = *p.RAM
	}
	if p.CPUs != nil {
		if *p.CPUs < 1 {
			return VM{}, fmt.Errorf("%w: cpus must be at least 1", ErrInvalidSpec)
		}
		v.CPUs = *p.CPUs
	}
	if p.Share != nil {
		v.Share = strings.TrimSpace(*p.Share)
	}
	if p.Recipes != nil {
		v.Recipes = *p.Recipes
	}

	if p.SSHPort != nil && *p.SSHPort != v.SSHPort {
		if err := validateSSHPort(v, *p.SSHPort); err != nil {
			return VM{}, err
		}
		v.SSHPort = *p.SSHPort
	}

	resizeTo := ""
	if p.Disk != nil && *p.Disk != v.Disk {
		resizeTo, err = validateDiskGrow(v, *p.Disk)
		if err != nil {
			return VM{}, err
		}
	}

	// The resize runs BEFORE the vm.toml write, mirroring edit.go's
	// saveEdit: if qemu-img fails, vm.toml is left describing the disk that
	// actually exists rather than one that doesn't.
	if resizeTo != "" {
		out, err := exec.Command("qemu-img", "resize", v.DiskPath(), resizeTo).CombinedOutput()
		if err != nil {
			return VM{}, fmt.Errorf("qemu-img resize: %s", strings.TrimSpace(string(out)))
		}
		v.Disk = resizeTo
	}

	if err := v.Save(); err != nil {
		return VM{}, err
	}
	return fromConfig(v), nil
}

// validateSSHPort checks a candidate new ssh port for name's VM against
// every rule that can make qemu fail unhelpfully or two VMs fight over one
// host listener, by reusing forward.go's validateForwards rather than
// re-deriving "every port any other VM has claimed" a second time.
//
// The trick: present the candidate as a synthetic PortForward (guest port
// 22, matching Args's own hardcoded ssh hostfwd; internal/qemu/args.go)
// appended to v's EXISTING forwards, and validate that list against v AS IT
// CURRENTLY STANDS, v.SSHPort not yet mutated to the candidate. That
// matters: validateForwards refuses a forward whose HostPort equals
// v.SSHPort (a forward colliding with the VM's own ssh port), and if v were
// already mutated to the candidate first, a caller asking to change the
// port to the same value it already had would spuriously trip that check
// against itself. Passing the OLD v.SSHPort means that check only fires for
// a genuine collision with some OTHER value, and the caller of
// validateSSHPort has already confirmed candidate != v.SSHPort before ever
// getting here, so "same value" is not a case this needs to special-case.
//
// This also incidentally catches a candidate colliding with one of v's OWN
// declared forwards. That case is checked explicitly, BEFORE handing off to
// validateForwards, for the message alone: validateForwards' own wording for
// it ("host port %d requested twice") is written for Forward, where a caller
// really did list the same port twice in one request. Reusing the LOGIC
// here is right; inheriting THAT message for an SSHPort caller, who
// supplied exactly one port and has no idea it was turned into a synthetic
// forward under the hood, would be actively misleading. Every other
// message validateForwards can produce ("host port N out of range", "needs
// root to bind", "already used by vm %q's ssh port/declared forward")
// already reads correctly as-is for a port that happens to be an ssh
// port, so those are passed through, wrapped only with an "ssh port:"
// prefix for context.
func validateSSHPort(v *config.VM, candidate int) error {
	for _, f := range v.Forwards {
		if f.HostPort == candidate {
			return fmt.Errorf("%w: ssh port %d collides with this vm's own forward to guest port %d",
				ErrInvalidSpec, candidate, f.GuestPort)
		}
	}
	synthetic := append(append([]config.PortForward(nil), v.Forwards...), config.PortForward{HostPort: candidate, GuestPort: 22})
	if err := validateForwards(v, synthetic); err != nil {
		return fmt.Errorf("ssh port: %w", err)
	}
	return nil
}

// validateDiskGrow checks a candidate disk size against every rule §2.1 and
// §8's mutability table impose on a disk edit, and returns the size to
// resize to (identical to the candidate, once ParseSize has normalised it)
// or an error. size == "" (empty candidate) is rejected by ParseSize itself
// ("empty"), which is correct here: Patch.Disk being non-nil means the
// caller asked for a change, and "" is not a valid disk size to change to.
func validateDiskGrow(v *config.VM, size string) (string, error) {
	// A live VM has no disk.qcow2 at all; internal/tui/edit.go's validate
	// refuses the mirror case (an empty size in disk/cloud mode) for the
	// same underlying reason: a field that names a size only means
	// something where a qcow2 exists.
	if v.Mode == "live" {
		return "", fmt.Errorf("%w: disk: live vms have no disk to resize", ErrInvalidSpec)
	}

	size = strings.TrimSpace(size)
	if _, err := ParseSize(size); err != nil {
		return "", fmt.Errorf("%w: disk size: %v", ErrInvalidSpec, err)
	}

	// §8's mutability table classes a disk grow as Destructive: "allowed
	// (stopped) / refused (running)": not merely deferred to next start
	// like RAM/CPUs/SSHPort, because unlike those, this function actually
	// runs qemu-img resize against the live file below. Reusing
	// ErrAlreadyRunning (rather than the design doc's own ErrRequiresStopped,
	// which, see the report accompanying this change, appears nowhere in
	// the doc's own §9 error taxonomy) matches how Destroy already signals
	// "this needs the VM stopped, and it isn't" for the same shape of
	// refusal.
	if qemu.Running(v) {
		return "", fmt.Errorf("%w: disk: stop %s before resizing its disk", ErrAlreadyRunning, v.Name)
	}

	// v.Disk is empty for a cloud VM that has never named a size of its own
	// (Create defaults it, so this is rare, but a hand-edited vm.toml or a
	// pre-default VM could still have one); ParseSize already rejected an
	// empty CANDIDATE above; an empty CURRENT size has nothing to compare
	// against, so any valid candidate is accepted as the new size rather
	// than compared.
	if v.Disk != "" {
		oldBytes, err := ParseSize(v.Disk)
		if err != nil {
			// v.Disk was written by a version of stoat that predates
			// ParseSize's relative-size rejection, or was hand-edited to
			// something ParseSize itself would now refuse. Either way,
			// there is no reliable old-vs-new comparison to make; refusing
			// the resize here (rather than guessing) is the same call
			// ParseSize's own callers already make everywhere else.
			return "", fmt.Errorf("%w: disk: current size %q: %v", ErrInvalidSpec, v.Disk, err)
		}
		newBytes, _ := ParseSize(size) // already validated above
		if newBytes < oldBytes {
			return "", fmt.Errorf("%w: %s -> %s", ErrDiskShrink, v.Disk, size)
		}
	}

	// disk.qcow2 does not exist yet for a cloud VM that has never started:
	// Create deliberately does not create the overlay (see core.go's own
	// comment on Create). qemu-img resize against a missing file fails with
	// a generic "Could not open"; naming the real reason here beats letting
	// that surface unexplained.
	if v.Mode == "cloud" {
		if _, err := os.Stat(v.DiskPath()); err != nil {
			return "", fmt.Errorf("%w: disk: start %s once before growing its disk", ErrInvalidSpec, v.Name)
		}
	}

	return size, nil
}
