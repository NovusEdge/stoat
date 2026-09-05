package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
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

	// AgentAccess sets vm.toml's agent_access. Safe: the MCP server reads it
	// fresh on every call, so it takes effect immediately, not at next
	// start. The MCP tool may only lower it; core applies whatever it is
	// given, since core is also the CLI and TUI's library, which may raise.
	AgentAccess *string
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
	work := cloneConfigVM(v)

	if err := checkImmutable(work, p); err != nil {
		return VM{}, err
	}

	if p.RAM != nil {
		if *p.RAM < 256 {
			return VM{}, fmt.Errorf("%w: ram must be at least 256 MB", ErrInvalidSpec)
		}
		work.RAM = *p.RAM
	}
	if p.CPUs != nil {
		if *p.CPUs < 1 {
			return VM{}, fmt.Errorf("%w: cpus must be at least 1", ErrInvalidSpec)
		}
		work.CPUs = *p.CPUs
	}
	if p.Share != nil {
		work.Share = strings.TrimSpace(*p.Share)
	}
	if p.Recipes != nil {
		work.Recipes = append([]string(nil), (*p.Recipes)...)
	}
	var stored config.Secrets
	if hasParamEdits(p) {
		stored, err = config.LoadSecrets(work.Dir)
		if err != nil {
			return VM{}, err
		}
	}
	var stagedSecrets config.Secrets
	var secretTouched bool
	err = stageParamEdits(work, p, stored, &stagedSecrets, &secretTouched)
	if err != nil {
		return VM{}, err
	}
	if p.Installed != nil {
		work.Installed = *p.Installed
	}
	if p.Display != nil {
		if err := validateDisplay(*p.Display); err != nil {
			return VM{}, err
		}
		work.Display = *p.Display
	}
	if p.AgentAccess != nil {
		work.AgentAccess = *p.AgentAccess
	}

	if p.SSHPort != nil && *p.SSHPort != work.SSHPort {
		if err := validateSSHPort(work, *p.SSHPort); err != nil {
			return VM{}, err
		}
		work.SSHPort = *p.SSHPort
	}

	resizeTo := ""
	if p.Disk != nil && *p.Disk != work.Disk {
		resizeTo, err = validateDiskGrow(work, *p.Disk)
		if err != nil {
			return VM{}, err
		}
	}

	// The resize runs before the vm.toml write, mirroring edit.go's
	// saveEdit. If qemu-img fails, vm.toml still describes the disk that
	// exists, not one that doesn't.
	if resizeTo != "" {
		out, err := exec.Command("qemu-img", "resize", work.DiskPath(), resizeTo).CombinedOutput()
		if err != nil {
			return VM{}, fmt.Errorf("qemu-img resize: %s", strings.TrimSpace(string(out)))
		}
		work.Disk = resizeTo
	}

	if err := commitUpdate(v, work, stagedSecrets, secretTouched); err != nil {
		return VM{}, err
	}
	return fromConfig(work), nil
}

// applyParamEdits validates and applies parameter changes. Non-secret values
// stay in vm.toml; secret values stay in secrets.toml and are removed when an
// unset edit names a secret parameter.
func applyParamEdits(v *config.VM, p Patch) error {
	stored, err := config.LoadSecrets(v.Dir)
	if err != nil {
		return err
	}
	var staged config.Secrets
	var touched bool
	if err := stageParamEdits(v, p, stored, &staged, &touched); err != nil {
		return err
	}
	if touched {
		return config.SaveSecrets(v.Dir, staged)
	}
	return nil
}

func stageParamEdits(v *config.VM, p Patch, stored config.Secrets, stagedOut *config.Secrets, touchedOut *bool) error {
	*stagedOut = cloneSecrets(stored)
	*touchedOut = false
	if len(p.SetParams) == 0 && len(p.UnsetParams) == 0 && len(p.Secrets) == 0 {
		return nil
	}

	for recipe, values := range p.SetParams {
		m, err := manifestForVM(v, recipe)
		if err != nil {
			return err
		}
		for name, value := range values {
			param, ok := m.Params[name]
			if !ok {
				if err := recipes.Validate(m, name, value); err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidSpec, err)
				}
			}
			if ok && param.Type == "secret" {
				return fmt.Errorf("%w: %s.%s is a secret; use --secret", ErrInvalidSpec, recipe, name)
			}
			if err := recipes.Validate(m, name, value); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidSpec, err)
			}
		}
	}

	secretTouched := len(p.Secrets) > 0
	for recipe, names := range p.UnsetParams {
		m, err := manifestForVM(v, recipe)
		if err != nil {
			return err
		}
		for _, name := range names {
			if _, ok := m.Params[name]; !ok {
				return fmt.Errorf("%w: %s.%s is not declared", ErrInvalidSpec, recipe, name)
			}
			if m.Params[name].Type == "secret" {
				secretTouched = true
			}
		}
	}
	for recipe, values := range p.Secrets {
		m, err := manifestForVM(v, recipe)
		if err != nil {
			return err
		}
		for name, value := range values {
			param, ok := m.Params[name]
			if !ok {
				return fmt.Errorf("%w: %s.%s is not declared", ErrInvalidSpec, recipe, name)
			}
			if param.Type != "secret" {
				return fmt.Errorf("%w: %s.%s is not a secret param", ErrInvalidSpec, recipe, name)
			}
			if value == "" {
				return fmt.Errorf("%w: %s.%s secret is empty", ErrInvalidSpec, recipe, name)
			}
		}
	}

	for recipe, values := range p.SetParams {
		for name, value := range values {
			v.SetParam(recipe, name, value)
		}
	}
	for recipe, names := range p.UnsetParams {
		m, _ := manifestForVM(v, recipe)
		for _, name := range names {
			if m.Params[name].Type != "secret" {
				v.UnsetParam(recipe, name)
			}
		}
	}
	staged := cloneSecrets(stored)
	if secretTouched {
		for recipe, names := range p.UnsetParams {
			m, _ := manifestForVM(v, recipe)
			for _, name := range names {
				if m.Params[name].Type == "secret" {
					delete(staged[recipe], name)
				}
			}
		}
		for recipe, values := range p.Secrets {
			if staged[recipe] == nil {
				staged[recipe] = map[string]string{}
			}
			for name, value := range values {
				staged[recipe][name] = value
			}
		}
	}
	*stagedOut = staged
	*touchedOut = secretTouched
	return nil
}

func hasParamEdits(p Patch) bool {
	return len(p.SetParams) > 0 || len(p.UnsetParams) > 0 || len(p.Secrets) > 0
}

func cloneSecrets(in config.Secrets) config.Secrets {
	if in == nil {
		return config.Secrets{}
	}
	out := make(config.Secrets, len(in))
	for recipe, values := range in {
		if values == nil {
			continue
		}
		out[recipe] = make(map[string]string, len(values))
		for name, value := range values {
			out[recipe][name] = value
		}
	}
	return out
}

func cloneConfigVM(in *config.VM) *config.VM {
	out := *in
	out.Recipes = append([]string(nil), in.Recipes...)
	out.Forwards = append([]config.PortForward(nil), in.Forwards...)
	if in.Params != nil {
		out.Params = make(map[string]map[string]string, len(in.Params))
		for recipe, values := range in.Params {
			out.Params[recipe] = make(map[string]string, len(values))
			for name, value := range values {
				out.Params[recipe][name] = value
			}
		}
	}
	return &out
}

// commitUpdate stages both on-disk representations, then swaps them into
// place with backups so a failure of the second replacement restores the
// first. The original inodes retain their modes and ownership on rollback.
func commitUpdate(original, updated *config.VM, secrets config.Secrets, secretTouched bool) error {
	stageDir, err := os.MkdirTemp(original.Dir, ".update-stage-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	stagedVM := cloneConfigVM(updated)
	stagedVM.Dir = stageDir
	if err := stagedVM.Save(); err != nil {
		return err
	}
	vmTarget := filepath.Join(original.Dir, "vm.toml")
	// The previous single-file Save opened vm.toml for writing, so a
	// read-only target failed even when its directory allowed replacement.
	// Probe that same permission boundary before the atomic swap; otherwise a
	// rename would silently bypass the target's mode and turn a failed update
	// into a successful one.
	if f, err := os.OpenFile(vmTarget, os.O_WRONLY, 0); err != nil {
		return err
	} else if err := f.Close(); err != nil {
		return err
	}
	if info, statErr := os.Stat(vmTarget); statErr == nil {
		if err := os.Chmod(filepath.Join(stageDir, "vm.toml"), info.Mode().Perm()); err != nil {
			return err
		}
	}

	stagedSecrets := filepath.Join(stageDir, config.SecretsName)
	secretTarget := filepath.Join(original.Dir, config.SecretsName)
	if secretTouched {
		if err := config.SaveSecrets(stageDir, secrets); err != nil {
			return err
		}
		if info, statErr := os.Stat(secretTarget); statErr == nil {
			if _, stageErr := os.Stat(stagedSecrets); stageErr == nil {
				if err := os.Chmod(stagedSecrets, info.Mode().Perm()); err != nil {
					return err
				}
			}
		}
	}

	vmBackup := filepath.Join(stageDir, "vm.toml.old")
	secretBackup := filepath.Join(stageDir, config.SecretsName+".old")
	vmHadOld := false
	secretHadOld := false
	vmInstalled := false
	secretInstalled := false
	rollback := func() {
		if secretInstalled {
			_ = os.Remove(secretTarget)
		}
		if vmInstalled {
			_ = os.Remove(vmTarget)
		}
		if secretHadOld {
			_ = os.Rename(secretBackup, secretTarget)
		}
		if vmHadOld {
			_ = os.Rename(vmBackup, vmTarget)
		}
	}

	if err := os.Rename(vmTarget, vmBackup); err != nil {
		return err
	}
	vmHadOld = true
	if secretTouched {
		if _, statErr := os.Stat(secretTarget); statErr == nil {
			if err := os.Rename(secretTarget, secretBackup); err != nil {
				rollback()
				return err
			}
			secretHadOld = true
		} else if !os.IsNotExist(statErr) {
			rollback()
			return statErr
		}
	}
	if err := os.Rename(filepath.Join(stageDir, "vm.toml"), vmTarget); err != nil {
		rollback()
		return err
	}
	vmInstalled = true
	if secretTouched {
		if _, statErr := os.Stat(stagedSecrets); statErr == nil {
			if err := os.Rename(stagedSecrets, secretTarget); err != nil {
				rollback()
				return err
			}
			secretInstalled = true
		} else if !os.IsNotExist(statErr) {
			rollback()
			return statErr
		}
	}
	return nil
}

func manifestForVM(v *config.VM, recipe string) (recipes.Manifest, error) {
	for _, name := range v.Recipes {
		if name != recipe {
			continue
		}
		m, ok, err := recipes.ManifestFor(recipe)
		if err != nil {
			return recipes.Manifest{}, err
		}
		if !ok {
			return recipes.Manifest{}, fmt.Errorf("%w: recipe %q has no recipe.toml", ErrRecipeNotApplicable, recipe)
		}
		return m, nil
	}
	return recipes.Manifest{}, fmt.Errorf("%w: %s is not one of %s's recipes", ErrRecipeNotApplicable, recipe, v.Name)
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
