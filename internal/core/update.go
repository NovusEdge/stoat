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

// ErrImmutableField is returned by Update when a Patch changes a field with no
// meaningful after-the-fact value (see Patch). Named so a caller that thinks it
// renamed a VM is told it did not, rather than getting a partial apply.
var ErrImmutableField = errors.New("immutable field")

// ErrDiskShrink is returned by Update when Patch.Disk names a size smaller than
// the VM's current one. Shrinking a qcow2 truncates it and corrupts the guest
// filesystem. ParseSize refuses a relative "+8G" for the adjacent reason:
// qemu-img resize reads a leading "+" as "grow by", which once turned an 8G
// disk into 16G.
var ErrDiskShrink = errors.New("disk can only grow")

// Patch describes an edit to an existing VM. Every field is a pointer so nil
// (leave alone) is distinct from the zero value (e.g. Share:"" clears a share),
// per design §2.1. The mutability classes come from §8:
//
//   - Name, OS, Backend, Mode: immutable. Present on the struct so a caller
//     round-tripping a full VM gets ErrImmutableField naming the field, rather
//     than a silently dropped field from a generic map (an MCP tool's JSON).
//     §2.1 makes Mode immutable only "after first boot"; checkImmutable holds
//     it immutable unconditionally, a narrowing explained there.
//   - RAM, CPUs, Share, SSHPort: safe, apply at next start.
//   - Recipes: safe, applies immediately; nothing reads it mid-boot.
//   - Disk: grow-only, absolute size only, refused while running (§8 classes a
//     grow as destructive).
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
	Recipes     *[]string
	SetParams   map[string]map[string]string
	UnsetParams map[string][]string
	Secrets     config.Secrets

	// Installed is meaningful only for a disk-mode VM: it tracks whether the OS
	// is installed to disk.qcow2 yet. qemu.Start flips it true once disk.qcow2
	// looks written; this field is the escape hatch when that guess is wrong,
	// so it is mutable, unlike Name/OS/Backend/Mode.
	Installed *bool

	// Display is the screen preference: "" or "auto", "window", or "vnc".
	// Safe: it only changes which -display argument qemu.Args builds next
	// start, nothing about the running process.
	Display *string
}

// checkImmutable reports ErrImmutableField, naming the field, when a Patch sets
// Name/OS/Backend/Mode to something other than the VM's current value. Setting
// one to its current value is not an error: an MCP tool patching back a full
// object must not be punished for fields it never meant to change.
//
// Mode is immutable unconditionally, not just "after first boot" as §2.1 says,
// because nothing answers "has this VM booted" across all three modes. A cloud
// VM's disk.qcow2 exists only after first start, but a disk VM's is created
// full by Create, and a live VM's apkovl overlay is rebuilt every start with no
// persisted artifact. Adding a fourth persisted flag was out of scope, so Mode
// stays immutable until a mode-agnostic "has started" signal exists.
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
// Held under config.Lock() across the whole read-modify-write, like
// Create/Clone/Destroy/Prune. Without the lock, two concurrent Updates can both
// see the same SSHPort as free and both claim it.
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
	if p.Installed != nil {
		v.Installed = *p.Installed
	}
	if p.Display != nil {
		if err := validateDisplay(*p.Display); err != nil {
			return VM{}, err
		}
		v.Display = *p.Display
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

	// The resize runs before the vm.toml write, mirroring edit.go's
	// saveEdit. If qemu-img fails, vm.toml still describes the disk that
	// exists, not one that doesn't.
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

// validateSSHPort checks a candidate ssh port by reusing validateForwards
// rather than re-deriving every claimed port. It presents the candidate as a
// synthetic PortForward (guest port 22, matching Args's ssh hostfwd) appended to
// v's existing forwards, and validates against v with the old SSHPort still in
// place. validateForwards refuses a forward whose HostPort equals v.SSHPort, so
// keeping the old port means that check fires only for a genuine collision, not
// against the port the caller is trying to set.
//
// A candidate colliding with one of v's own forwards is checked explicitly
// first, for the message alone: validateForwards says "requested twice", which
// misleads an SSHPort caller who never saw the synthetic forward. Every other
// validateForwards message reads correctly for an ssh port, so it passes them
// through under an "ssh port:" prefix.
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

// validateDiskGrow checks a candidate disk size against every rule §2.1
// and §8's mutability table impose on a disk edit. It returns the
// normalised size to resize to, or an error.
//
// ParseSize rejects an empty candidate with "empty". That is correct
// here: a non-nil Patch.Disk means the caller asked for a change, and ""
// is not a valid size to change to.
func validateDiskGrow(v *config.VM, size string) (string, error) {
	// A live VM has no disk.qcow2. internal/tui/edit.go's validate refuses
	// the mirror case, an empty size in disk or cloud mode, for the same
	// reason: a size only means something where a qcow2 exists.
	if v.Mode == "live" {
		return "", fmt.Errorf("%w: disk: live vms have no disk to resize", ErrInvalidSpec)
	}

	size = strings.TrimSpace(size)
	if _, err := ParseSize(size); err != nil {
		return "", fmt.Errorf("%w: disk size: %v", ErrInvalidSpec, err)
	}

	// §8 classes a disk grow as destructive: allowed stopped, refused running,
	// because qemu-img resize runs against the live file below. RAM, CPUs and
	// SSHPort defer to next start instead. ErrAlreadyRunning matches how Destroy
	// signals the same "needs stopped, isn't" refusal.
	if qemu.Running(v) {
		return "", fmt.Errorf("%w: disk: stop %s before resizing its disk", ErrAlreadyRunning, v.Name)
	}

	// v.Disk can be empty for a cloud VM that never named its own size.
	// Create defaults it, so this is rare; a hand-edited vm.toml or a
	// pre-default VM can still have one. ParseSize already rejected an
	// empty candidate above. An empty current size has nothing to compare
	// against, so any valid candidate is accepted without comparison.
	if v.Disk != "" {
		oldBytes, err := ParseSize(v.Disk)
		if err != nil {
			// v.Disk may predate ParseSize's relative-size rejection, or
			// be hand-edited to something ParseSize now refuses. Either
			// way there is no reliable old-vs-new comparison. Refusing
			// the resize matches what every other ParseSize caller does
			// in this situation.
			return "", fmt.Errorf("%w: disk: current size %q: %v", ErrInvalidSpec, v.Disk, err)
		}
		newBytes, _ := ParseSize(size) // already validated above
		if newBytes < oldBytes {
			return "", fmt.Errorf("%w: %s -> %s", ErrDiskShrink, v.Disk, size)
		}
	}

	// disk.qcow2 does not exist for a cloud VM that has never started;
	// Create deliberately skips the overlay (see Create's comment in
	// core.go). qemu-img resize against a missing file fails with a
	// generic "Could not open". Naming the real reason here is clearer.
	if v.Mode == "cloud" {
		if _, err := os.Stat(v.DiskPath()); err != nil {
			return "", fmt.Errorf("%w: disk: start %s once before growing its disk", ErrInvalidSpec, v.Name)
		}
	}

	return size, nil
}
