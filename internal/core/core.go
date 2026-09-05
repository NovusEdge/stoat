// Package core is stoat's operation surface: everything that can be done to a
// VM, callable with no interactive input, returning structured data.
//
// Image resolution, OS inference, backend choice, port allocation, vm.toml
// and the qcow2 used to live only in internal/tui's Bubbletea form. The CLI
// had no create command for that reason. core replaces both call sites; the
// TUI, the CLI and an MCP server all sit on top of it.
//
// See docs/design/core-api.md for the full intended surface. Only what has a
// caller is built.
package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

// Typed errors, because every caller branches on them and string matching is
// how that goes wrong. Each wraps with the specific subject.
var (
	ErrNotFound           = errors.New("not found")
	ErrNameTaken          = errors.New("name already taken")
	ErrImageNotDownloaded = errors.New("image not downloaded")
	ErrInvalidSpec        = errors.New("invalid spec")
	ErrInUse              = errors.New("in use")
	// ErrRecipeNotApplicable: a recipe was named that this VM's OS and
	// backend cannot run. Typed because a caller retrying with a corrected
	// name needs to tell this apart from a malformed spec.
	ErrRecipeNotApplicable = errors.New("recipe not applicable")
)

// Defaults a Spec's zero values fall back to. They are the same values the
// new-VM form pre-fills, so a Spec with nothing but a name and an image
// produces the VM a user would have got by pressing enter through the form.
const (
	DefaultRAM  = 4096
	DefaultCPUs = 4
	DefaultDisk = "8G"
)

// Spec is a VM to create, declaratively.
type Spec struct {
	Name string
	// Image is a catalog entry ID ("alpine-virt"), a bare filename under
	// isos/, or an absolute path to a bring-your-own image anywhere on disk.
	Image string

	// OS and Backend override what the filename implies. They apply to BYO
	// images only; a catalog entry states both, and is authoritative.
	OS      string
	Backend string

	// Mode is honoured for the apkovl backend only ("live" or "disk"); every
	// other backend has exactly one sensible mode and picks it. Empty means
	// "live", matching the form's default.
	Mode string

	RAM     int
	CPUs    int
	Disk    string // qemu-img size, absolute only ("8G", never "+8G")
	Share   string
	Recipes []string
	Params  map[string]map[string]string
	Secrets config.Secrets

	// Project is the absolute directory of the stoat.toml that declared this
	// VM. Empty for stoat new. plan writes it to vm.toml unchanged.
	Project string

	// Shares are the project's 9p exports, already resolved and checked by
	// project.Shares. plan does not validate them again: containment is a
	// question about the project directory, which core cannot see.
	Shares []config.Share

	// Display is the screen preference to record in vm.toml: "" or "auto"
	// (default), "window", or "vnc". validateDisplay is the single check
	// for the value; plan applies it.
	Display string

	// ConsolePassword: empty means config.DefaultConsolePassword, "random"
	// generates one, anything else is used verbatim. Written for the
	// cloudinit backend only; see config.VM.ConsolePassword for why no
	// other backend needs one.
	ConsolePassword string

	// AllowExec is a per-VM opt-out of exec/copy_to/copy_from, recorded in
	// vm.toml at create time. core.Exec does not enforce it: core is a
	// library the TUI and CLI also call, and both already let a user run
	// anything. Enforcement belongs to the boundary an agent crosses, the
	// MCP server.
	//
	// AllowExec is a pointer, like config.Patch's fields. Nil means "not
	// given", so plan() can default it to true for a caller that never
	// mentions it, such as the TUI's form today.
	AllowExec *bool

	// AgentAccess is the MCP create tool's agent_access input, recorded on
	// the new VM. Empty means "manage", plan()'s default. The MCP server
	// validates the string against its Level enum before calling Create;
	// core stores whatever it is given.
	AgentAccess string
}

// Create validates a Spec, writes vm.toml and allocates the disk. It does not
// start the VM.
//
// Create does not build a cloud VM's overlay or cloud-init seed. A cloud
// overlay holds real guest state, unlike a live VM's apkovl, which qemu.Start
// rebuilds every start. qemu.Start's ensureCloudOverlay creates the overlay
// once, on first start, so a VM that never starts never gets one.
//
// Create returns the same VM view Get and List hand out. Name here is the
// VM's directory, not vm.toml's own name field.
func Create(s Spec) (VM, error) {
	// The lock covers plan and Save together. plan allocates an ssh port and
	// checks the name is free, but neither is committed until Save writes
	// vm.toml. Two callers interleaved in that gap would pick the same port
	// or claim the same name. Plan on its own holds no lock: it is a dry run
	// for a caller that wants to show an error early, such as the TUI form.
	// Create re-plans under the lock instead of trusting that result.
	unlock, err := config.Lock()
	if err != nil {
		return VM{}, err
	}
	defer unlock()

	v, err := plan(s)
	if err != nil {
		return VM{}, err
	}
	if err := v.Save(); err != nil {
		return VM{}, err
	}
	if err := applyParamEdits(v, Patch{SetParams: s.Params, Secrets: s.Secrets}); err != nil {
		_ = os.RemoveAll(v.Dir)
		return VM{}, err
	}
	if len(s.Params) > 0 {
		if err := v.Save(); err != nil {
			_ = os.RemoveAll(v.Dir)
			return VM{}, err
		}
	}
	if v.Mode == "disk" {
		out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), v.Disk).CombinedOutput()
		if err != nil {
			// Leave no trace of a failed creation: otherwise the list shows a
			// VM with no disk.qcow2 that can never boot.
			_ = os.RemoveAll(v.Dir)
			return VM{}, fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
		}
	}
	return fromConfig(v), nil
}

// Plan is Create without side effects: it validates a Spec and returns the
// VM that Create would write. A caller that wants to show an error inline,
// next to the field that caused it, such as a form, checks with Plan first.
//
// Plan holds all validation and resolution logic. Tests can exercise it
// without writing to the data root.
func Plan(s Spec) (*config.VM, error) { return plan(s) }

func plan(s Spec) (*config.VM, error) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidSpec)
	}
	if strings.ContainsAny(name, "/ ") {
		return nil, fmt.Errorf("%w: name cannot contain spaces or slashes", ErrInvalidSpec)
	}
	if _, err := os.Stat(filepath.Join(config.Root(), name)); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrNameTaken, name)
	}

	img, err := resolveImage(s.Image)
	if err != nil {
		return nil, err
	}
	img = img.apply(s)

	mode, err := modeFor(img.backend, s.Mode)
	if err != nil {
		return nil, err
	}

	ram := s.RAM
	if ram == 0 {
		ram = DefaultRAM
	}
	if ram < 256 {
		return nil, fmt.Errorf("%w: ram must be at least 256 MB", ErrInvalidSpec)
	}
	cpus := s.CPUs
	if cpus == 0 {
		cpus = DefaultCPUs
	}
	if cpus < 1 {
		return nil, fmt.Errorf("%w: cpus must be at least 1", ErrInvalidSpec)
	}

	// A relative size ("+8G") reads to qemu-img as "grow by", not "resize
	// to". `qemu-img create -f qcow2 disk.qcow2 +8G` then silently allocates
	// twice the requested size. ParseSize refuses a relative size; the edit
	// path calls the same function and gets the same refusal.
	disk := strings.TrimSpace(s.Disk)
	// Disk mode and cloud mode both get a default size; live mode does not.
	//
	// A cloud VM's qcow2 is a CoW overlay. The overlay inherits the base
	// image's virtual size. Ubuntu 24.04's cloud image is 3.5G with a 2.4G
	// root. backend/cloudinit.go resizes the overlay only when Disk is set.
	// With no size, installing a desktop fills the disk. apt then exits 100,
	// and cloud-init reports a bare "error" that reads as a broken recipe,
	// not a full disk.
	//
	// The TUI's form pre-fills 8G for every mode, so a cloud VM built there
	// always carried a size. Defaulting only disk mode here made the CLI
	// produce a different VM from the TUI for the same request.
	//
	// Live mode has no qcow2: it boots the ISO into a tmpfs root.
	if disk == "" && mode != "live" {
		disk = DefaultDisk
	}
	if disk != "" {
		if _, err := ParseSize(disk); err != nil {
			return nil, fmt.Errorf("%w: disk size: %v", ErrInvalidSpec, err)
		}
	}

	if err := checkRecipes(img.osName, img.backend, s.Recipes); err != nil {
		return nil, err
	}
	if err := CheckDependencies(s.Recipes); err != nil {
		return nil, err
	}

	if err := validateDisplay(s.Display); err != nil {
		return nil, err
	}

	port, err := config.FreePort()
	if err != nil {
		return nil, err
	}

	// Default true: an agent should be able to exec in a VM it just created
	// unless the caller explicitly said otherwise. See Spec.AllowExec's doc
	// comment for why this is a pointer rather than a plain bool.
	allowExec := true
	if s.AllowExec != nil {
		allowExec = *s.AllowExec
	}
	agentAccess := s.AgentAccess
	if agentAccess == "" {
		agentAccess = "manage"
	}

	v := &config.VM{
		Name:        name,
		Mode:        mode,
		OS:          img.osName,
		Backend:     img.backend,
		SSHUser:     img.sshUser,
		RAM:         ram,
		CPUs:        cpus,
		Disk:        disk,
		Share:       strings.TrimSpace(s.Share),
		SSHPort:     port,
		Recipes:     s.Recipes,
		AllowExec:   allowExec,
		AgentAccess: agentAccess,
		Display:     s.Display,
	}

	if img.backend == "cloudinit" {
		v.Base = img.abs
		// Only a cloud image needs a console password. cloud-init locks
		// every account by default, so its console (a qemu window, or VNC
		// on a headless host; see qemu.NeedsWindow) shows a login prompt
		// with no valid answer otherwise. A live Alpine VM logs root in at
		// the console with no password. A disk VM's password is whatever
		// the user set in the guest's own installer.
		pw := config.DefaultConsolePassword
		switch s.ConsolePassword {
		case "":
		case "random":
			if pw, err = config.RandomConsolePassword(); err != nil {
				return nil, err
			}
		default:
			pw = s.ConsolePassword
		}
		v.ConsolePassword = pw
	} else {
		v.ISO = img.isoField()
	}
	return v, nil
}

// modeFor decides boot media. cloudinit is always "cloud": a cloud image
// boots straight off its overlay, with no install step. ssh is always
// "disk": an unrecognised image is assumed to need a real install, then ssh.
// The apkovl live path exists only for Alpine. apkovl keeps the caller's
// live/disk choice.
func modeFor(backend, want string) (string, error) {
	switch backend {
	case "cloudinit":
		if want != "" && want != "cloud" {
			return "", fmt.Errorf("%w: a cloud image is always cloud mode, not %q", ErrInvalidSpec, want)
		}
		return "cloud", nil
	case "ssh":
		if want != "" && want != "disk" {
			return "", fmt.Errorf("%w: this image is always disk mode, not %q", ErrInvalidSpec, want)
		}
		return "disk", nil
	default:
		switch want {
		case "":
			return "live", nil
		case "live", "disk":
			return want, nil
		}
		return "", fmt.Errorf("%w: mode must be live or disk, not %q", ErrInvalidSpec, want)
	}
}

// ParseSize reads qemu-img's size syntax ("8G", "512M") as bytes. Only the
// suffixes qemu-img itself documents are accepted, and a relative size is
// refused outright.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	// qemu-img accepts relative sizes ("+8G"), and strconv.ParseFloat reads
	// "+8" as 8. Unchecked, "+8G" would pass as "8G" then ADD 8G, leaving
	// vm.toml recording "+8G" while the actual disk disagrees. ParseSize
	// refuses a relative size instead of translating it.
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("use an absolute size like 16G, not a relative one")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1<<10, s[:len(s)-1]
	case 'M':
		mult, s = 1<<20, s[:len(s)-1]
	case 'G':
		mult, s = 1<<30, s[:len(s)-1]
	case 'T':
		mult, s = 1<<40, s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("use a size like 8G or 512M")
	}
	return int64(n * float64(mult)), nil
}

// checkRecipes refuses a Spec naming a recipe this VM cannot run, at CREATE
// time rather than at first start.
//
// recipes.List returns the names offered for osName (recipe directory names
// like "xfce"), filtered by each manifest's OS and Requires. Before this
// check, `stoat create x --recipes xfce` was accepted and written to vm.toml,
// then failed only on `stoat up` with "open .../recipes/xfce: no such file or
// directory".
//
// The error names the recipes that are available. The failure is usually a
// name that is close but not exact, and a caller cannot always read the
// recipes directory to guess again. An agent caller never can.
//
// This is the cheap half of design/core-api.md §4's CheckRecipes. It checks
// that a recipe exists for this OS and backend. It does not check a recipe's
// own declared requirements; that needs the recipe contract in
// guest-subsystem.md §5.
func checkRecipes(osName, backend string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	available, err := recipes.List(osName, backend)
	if err != nil {
		return err
	}
	ok := make(map[string]bool, len(available))
	for _, a := range available {
		ok[a] = true
	}
	for _, n := range names {
		if !ok[n] {
			if len(available) == 0 {
				return fmt.Errorf("%w: recipe %q: no recipes are available for %s/%s",
					ErrRecipeNotApplicable, n, osName, backend)
			}
			return fmt.Errorf("%w: recipe %q is not available for %s/%s; available: %s",
				ErrRecipeNotApplicable, n, osName, backend, strings.Join(available, ", "))
		}
		if m, mok, err := recipes.ManifestFor(n); err == nil && mok && m.Stage == "install" {
			return fmt.Errorf("%w: recipe %q: %s", ErrRecipeNotApplicable, n, installStageUnsupported)
		}
	}
	return nil
}

// installStageUnsupported is the reason install-stage recipes are refused at
// add time. The stage field parses and validates, but no backend runs an
// install-stage body yet; BYO ISO support will add the hooks (docs/specs
// recipe-system-fixes §6).
const installStageUnsupported = "install-stage recipes are not yet supported"
