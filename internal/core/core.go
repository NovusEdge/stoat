// Package core is stoat's operation surface: everything that can be done to a
// VM, callable with no interactive input and returning structured data.
//
// It exists because creating a VM used to be possible only by driving a
// Bubbletea form: image resolution, OS inference, backend choice, port
// allocation, vm.toml and the qcow2 all lived in internal/tui. The CLI had no
// create command for exactly that reason, and an MCP server would have had to
// reimplement it. The TUI, the CLI and an MCP server all sit on top of this.
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

	// ConsolePassword: empty means config.DefaultConsolePassword, "random"
	// generates one, anything else is used verbatim. Written for the
	// cloudinit backend only; see config.VM.ConsolePassword for why no
	// other backend needs one.
	ConsolePassword string
}

// Create validates a Spec, writes vm.toml and allocates the disk. It does not
// start the VM.
//
// A cloud VM's overlay (backed by Base) and its cloud-init seed are
// deliberately NOT created here: qemu.Start's ensureCloudOverlay creates them
// once, on first start, since, unlike a live VM's apkovl, rebuilt every start,
// a cloud overlay holds real guest state that must never be discarded, and
// creating it here would also mean creating it again for a VM that is never
// started.
func Create(s Spec) (*config.VM, error) {
	// Held across plan AND Save. plan allocates an ssh port and checks the
	// name is free; neither is committed until Save writes vm.toml, so two
	// callers interleaved in that gap both pick the same port and both believe
	// the name is theirs. Plan on its own is deliberately NOT locked: it is a
	// dry run for callers that want to show an error early (the TUI form), and
	// Create re-plans under the lock rather than trusting that result.
	unlock, err := config.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	v, err := plan(s)
	if err != nil {
		return nil, err
	}
	if err := v.Save(); err != nil {
		return nil, err
	}
	if v.Mode == "disk" {
		out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), v.Disk).CombinedOutput()
		if err != nil {
			// Leave no trace of a failed creation: otherwise the list shows a
			// VM with no disk.qcow2 that can never boot.
			os.RemoveAll(v.Dir)
			return nil, fmt.Errorf("qemu-img: %s", strings.TrimSpace(string(out)))
		}
	}
	return v, nil
}

// Plan is Create without any side effects: it validates a Spec and returns the
// VM that Create would write. A caller with somewhere better to show an error
// than Create's return value, a form that wants it inline, next to the field
// that caused it, checks with Plan first.
//
// It is also the whole of validation and resolution, testable without writing
// to the data root.
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

	// A relative size ("+8G") reads to qemu-img as "grow by", not "resize to",
	// so `qemu-img create -f qcow2 disk.qcow2 +8G` silently allocates twice
	// what was asked for. ParseSize refuses it; the edit path refuses it the
	// same way through the same function.
	disk := strings.TrimSpace(s.Disk)
	// Every mode that HAS a disk gets a size, which is both of them that do.
	//
	// Cloud mode needs this as much as disk mode, for a reason that is not
	// obvious: its qcow2 is a CoW overlay, and an overlay inherits its BASE
	// image's virtual size. Cloud images are sized to boot and nothing more:
	// Ubuntu 24.04's is 3.5G with a 2.4G root, so without a size the overlay
	// is created and never resized (backend/cloudinit.go only resizes when
	// Disk is set), installing a desktop fills the disk, apt exits 100, and
	// cloud-init reports a bare "error" that reads as a broken recipe rather
	// than a full disk. That failure was diagnosed and fixed once already.
	//
	// The TUI never hit it because its form pre-fills 8G on every mode, so a
	// cloud VM built there always carried a size. Defaulting only disk mode
	// here made the CLI produce a DIFFERENT VM from the TUI for the same
	// request, precisely the divergence this package exists to remove.
	//
	// Live mode is excluded because it is genuinely diskless: an ISO booted
	// into a tmpfs root, with no qcow2 to size.
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

	port, err := config.FreePort()
	if err != nil {
		return nil, err
	}

	v := &config.VM{
		Name:    name,
		Mode:    mode,
		OS:      img.osName,
		Backend: img.backend,
		SSHUser: img.sshUser,
		RAM:     ram,
		CPUs:    cpus,
		Disk:    disk,
		Share:   strings.TrimSpace(s.Share),
		SSHPort: port,
		Recipes: s.Recipes,
	}

	if img.backend == "cloudinit" {
		v.Base = img.abs
		// Only a cloud image needs this. cloud-init locks every account by
		// default, so without a console password the VNC console (a cloud VM
		// never gets a qemu window, qemu.NeedsWindow) shows a login prompt
		// with no valid answer. A live Alpine VM already logs root in at the
		// console with no password, and a disk VM's password is whatever the
		// user sets during the guest's own installer.
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

// modeFor decides boot media. cloudinit is always "cloud" (a cloud image boots
// straight off its overlay, no install step); ssh is always "disk" (an
// unrecognised image is assumed to need a real install, then ssh (the apkovl
// live path only exists for Alpine.) apkovl keeps the caller's live/disk
// choice.
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
	// qemu-img also accepts relative sizes ("+8G"), and strconv.ParseFloat
	// happily reads "+8" as 8, so "+8G" would pass a grow check as "8G" and
	// then ADD 8G, leaving vm.toml recording "+8G" and disagreeing with the
	// disk forever after. Refused rather than translated.
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

// checkRecipes refuses a Spec naming a recipe this VM will not be able to run,
// at CREATE time rather than at first start.
//
// recipes.List returns FULL FILENAMES ("xfce.cloud.yaml"), and that is what
// recipes.Read expects, because the suffix is load-bearing: it separates
// ssh-pushed shell recipes from cloud-init seed fragments, and a per-OS
// fragment from the shared one. Nothing enforced that a Spec's names came from
// that list, so `stoat create x --recipes xfce` was accepted, written to
// vm.toml, and only failed on `stoat up` with "open .../recipes/xfce: no such
// file or directory", a create that succeeded and produced a VM that cannot
// start.
//
// The error names what IS available, because the failure is nearly always a
// name that is close but not exact, and a caller that cannot see the valid set
// has to go read a directory to guess again. That matters most for the caller
// who cannot read a directory: an agent.
//
// This is the cheap half of the design's CheckRecipes (docs/design/core-api.md
// §4). It checks that a recipe EXISTS for this OS and backend; it does not yet
// check a recipe's own declared requirements, which needs the recipe contract
// in guest-subsystem.md §5 to exist first.
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
	}
	return nil
}
