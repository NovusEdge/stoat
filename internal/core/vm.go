package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/recipes"
)

// State is List/Get's answer at call time. It is never cached; it comes
// fresh from the process table and the filesystem each time.
//
// The design doc (docs/design/core-api.md §1) lists six states. Only three
// are knowable today. StateStopped and StateRunning come from qemu.Running,
// which checks pid liveness and matches /proc/<pid>/cmdline, so a reused pid
// never reads as running. StateBroken comes from a vm.toml that exists but
// fails to parse (config.ListBroken's concept).
//
// StateStarting, StateApplying and StateFailed are not declared here. No
// code path yet distinguishes "qemu process is up" from "guest is
// reachable", tracks a mid-apply recipe run, or records a failed operation.
// A caller that switched on State exhaustively would get three dead cases.
// Add these constants when Start/Apply gain the tracking to back them.
type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateBroken  State = "broken"
)

// States returns every State this package produces. Three of the design
// doc's six; see State's own comment for why the other three are absent.
func States() []State { return []State{StateStopped, StateRunning, StateBroken} }

// ErrNotRunning is returned by Stop (and by Destroy, transitively) for a VM
// that is not running. qemu.Stop itself treats "already stopped" as a
// no-op; Stop checks first so the caller gets a typed error instead.
var ErrNotRunning = errors.New("not running")

// ErrAlreadyRunning is returned by Start for a VM that is already running,
// and by Destroy for a VM that is still running. Both refuse for the same
// reason: the operation needs the VM stopped.
var ErrAlreadyRunning = errors.New("already running")

// ErrBroken is returned by Start/Stop/Destroy for a VM whose vm.toml exists
// but fails to parse. Get and List do NOT return this: a broken VM must be
// visible to those two (as StateBroken, so it can still be found and
// destroyed) rather than looking deleted. Start/Stop/Destroy have no VM view
// to attach a state to, so a typed error is the only way to say "broken" to
// those callers.
var ErrBroken = errors.New("broken vm.toml")

// Paths are the on-disk locations for one VM, resolved once here so a caller
// (an MCP server describing a VM, a TUI detail screen) does not have to
// import config and call five separate methods to get them. Every field
// comes straight from the matching config.VM path method: ApplyLog from
// v.ProvisionLogPath(), the same one access.go's Logs(WhichApply) opens.
type Paths struct {
	Dir           string
	Disk          string
	ConsoleLog    string
	ApplyLog      string
	VNCSocket     string
	MonitorSocket string
}

// AppliedRecipe mirrors config.AppliedRecipe: which version of a recipe ran
// against a VM, the hash of the script that ran, and when. Defined here
// rather than re-exported from config, because core is the headless layer
// the CLI, the TUI and the MCP server all depend on, and a type alias would
// leak config as part of that contract.
type AppliedRecipe struct {
	Version string
	Hash    string
	At      time.Time
	Health  string
	Outputs map[string]string
}

// SecretSet and SecretUnset are the redacted states readers expose for
// recipe secret parameters.
const (
	SecretSet   = "<set>"
	SecretUnset = "<unset>"
)

// RecipeState is one recipe's stored per-VM state. Secret values are never
// carried here; SecretNames lets a wire boundary verify redaction.
type RecipeState struct {
	Name        string
	Applied     bool
	Version     string
	At          time.Time
	Health      string
	Params      map[string]string
	SecretNames []string
	Outputs     map[string]string
}

// VM answers "what is this VM doing right now". It is not the on-disk
// record. config.VM is vm.toml: what was asked for, valid the instant it was
// last saved. It says nothing about "is it running" on its own; that needs
// qemu.Running too. core.VM combines both, computed together, so a caller
// never has one without the other.
//
// The design doc's VM (§1) also lists Progress and Created. Progress is
// missing because nothing produces one yet; see State's comment. Created is
// missing because nothing records a creation timestamp: vm.toml's mtime
// moves on every Update or recipe-list edit, so it does not serve.
type VM struct {
	// Name is the VM's DIRECTORY under the data root, which is its identity;
	// see the note above load(). It is NOT necessarily the `name` field in
	// vm.toml; when those diverge, this is the one that works.
	Name    string
	OS      string
	Mode    string // live | disk | cloud
	Backend string // apkovl | cloudinit | ssh
	State   State

	// StartedAt is the running QEMU process's start time, taken from its
	// pidfile's mtime. It is zero when State is not StateRunning. This holds
	// a timestamp, not a duration: List is a snapshot with no periodic
	// refresh, so a caller wanting "up 5m32s" computes it with time.Since,
	// which keeps ticking between reloads.
	StartedAt time.Time

	RAM          int
	CPUs         int
	Disk         string
	Share        string
	Recipes      []string
	RecipeStates []RecipeState
	Health       Health

	SSHPort int
	SSHUser string

	// Display is the user's screen preference: "" or "auto" (default),
	// "window", or "vnc". DisplayKind and DisplayFor turn this, plus the
	// host's graphical session, into where the screen actually lands.
	Display string

	// ISO and Base are plain vm.toml facts. They are here because a caller
	// asking what a VM IS needs them, and without them the TUI would have to
	// keep a second config.Load beside every core.Get.
	ISO  string
	Base string

	// ConsolePassword is the password for the VNC console, which is the only
	// way into a cloud VM whose accounts are otherwise locked. A user cannot
	// use it without being shown it, so it is here.
	//
	// It must NEVER reach a wire format. The CLI's JSON DTO omits it, and that
	// omission is the reason the DTO layer exists rather than json tags on
	// this type.
	ConsolePassword string

	// Forwards are the user-declared host->guest port forwards. This field,
	// not a separate accessor, is the only way to read them: a caller that
	// called Get already has everything, with no separate round trip that
	// could go stale against the State next to it.
	Forwards []PortForward

	// Applied tracks which recipes have already run on this VM, keyed by
	// recipe name, mirroring config.VM.Applied. A VM with none gets a nil
	// map, which reads fine with len() and range.
	Applied map[string]AppliedRecipe

	Installed bool

	// AllowExec is vm.toml's own allow_exec, already resolved by
	// config.Load so an absent key reads as true here too. See
	// Spec.AllowExec's doc comment for who enforces it.
	AllowExec bool

	// AgentAccess is vm.toml's agent_access, already resolved by config.Load
	// so a pre-existing vm.toml with no agent_access key reads as "manage"
	// (or "exec", mapped from a legacy allow_exec = true) here too.
	AgentAccess string

	Paths Paths

	// Error is populated only when State is StateBroken, and holds
	// config.Load's parse error so a caller can show the user why, not just
	// that it's broken.
	Error string
}

// A VM's IDENTITY is its DIRECTORY under the data root. It is never the
// `name` field inside its vm.toml. The two can diverge: editing a VM keeps
// its directory and can leave a different name in the file. Everything that
// acts on a VM is directory-anchored: config.Load builds Root()/<name>/vm.toml,
// qemu's pidfile and monitor socket come off v.Dir, and config.VM.Delete
// removes v.Dir. internal/tui's TestDeleteTargetsDirectoryNotName pins the
// same rule for the delete path.
//
// Every `name string` parameter in this package is a DIRECTORY name.
// VM.Name must report that same directory. Otherwise List hands back an
// identifier that Get and Destroy reject, or one that resolves to a
// different VM: a directory "work" holding `name = "work2"` once made
// List()[0].Name return "work2", and Get("work2") then failed with
// ErrNotFound. Pinned by TestIdentityFromListRoundTrips.

// load resolves a VM DIRECTORY name to its config.VM. It separates two
// failures config.Load conflates into one bare decode/stat error: no such VM
// at all (ErrNotFound) versus a directory whose vm.toml exists but won't
// parse (ErrBroken). Start/Stop/Destroy need that distinction to give a
// broken VM a real error instead of a raw TOML parse message.
func load(name string) (*config.VM, error) {
	if _, err := os.Stat(filepath.Join(config.Root(), name, "vm.toml")); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	v, err := config.Load(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBroken, name, err)
	}
	if err := checkGuest(v); err != nil {
		return nil, err
	}
	return v, nil
}

// checkGuest reports ErrUnknownGuest, wrapped in ErrBroken, when v.OS names
// no loaded guest. An empty OS (a live/disk VM predating the OS field, or one
// that never set it) is not checked: emptiness has its own fallbacks
// elsewhere and is not a broken state.
func checkGuest(v *config.VM) error {
	if v.OS == "" {
		return nil
	}
	if _, ok := guest.Lookup(v.OS); !ok {
		return fmt.Errorf("%w: %s: %w %q; run stoat guest ls", ErrBroken, filepath.Base(v.Dir), ErrUnknownGuest, v.OS)
	}
	return nil
}

// fromConfig builds the point-in-time view for a VM that parsed cleanly.
// State and Paths are the two things config.VM cannot answer for itself.
func fromConfigUnchecked(v *config.VM) VM {
	state := StateStopped
	if qemu.Running(v) {
		state = StateRunning
	}
	osName, backend := inferMissing(v)
	return VM{
		// The DIRECTORY, not v.Name; see the identity note above load(). Every
		// other operation in this package accepts this identifier, so it is
		// the only one List may hand out.
		Name:            filepath.Base(v.Dir),
		OS:              osName,
		Mode:            v.Mode,
		Backend:         backend,
		State:           state,
		StartedAt:       qemu.StartedAt(v),
		RAM:             v.RAM,
		CPUs:            v.CPUs,
		Disk:            v.Disk,
		Share:           v.Share,
		Recipes:         v.Recipes,
		Applied:         applied(v.Applied),
		SSHPort:         v.SSHPort,
		SSHUser:         v.SSHUser,
		Display:         v.Display,
		ISO:             v.ISO,
		Base:            v.Base,
		ConsolePassword: v.ConsolePassword,
		Forwards:        v.Forwards,
		Installed:       v.Installed,
		AllowExec:       v.AllowExec,
		AgentAccess:     v.AgentAccess,
		Paths: Paths{
			Dir:           v.Dir,
			Disk:          v.DiskPath(),
			ConsoleLog:    v.ConsoleLogPath(),
			ApplyLog:      v.ProvisionLogPath(),
			VNCSocket:     v.VNCPath(),
			MonitorSocket: v.MonitorPath(),
		},
	}
}

// fromConfigChecked adds the stored recipe state that requires reading the
// protected secret store. Public readers use this form so a security error
// cannot be mistaken for an empty VM state.
func fromConfigChecked(v *config.VM) (VM, error) {
	out := fromConfigUnchecked(v)
	states, err := recipeStates(v)
	if err != nil {
		return VM{}, fmt.Errorf("%s: %w", filepath.Base(v.Dir), err)
	}
	out.RecipeStates = states
	verdicts := make([]RecipeHealth, 0, len(states))
	for _, state := range states {
		status := HealthUnknown
		if state.Health != "" {
			status = Health(state.Health)
		}
		verdicts = append(verdicts, RecipeHealth{Name: state.Name, Status: status})
	}
	out.Health = VMHealth(verdicts)
	return out, nil
}

// fromConfig retains the value-only helper used by mutating operations.
// Public Get and List call fromConfigChecked and propagate secret-store
// failures instead.
func fromConfig(v *config.VM) VM {
	out, _ := fromConfigChecked(v)
	return out
}

// recipeStates projects one state for every configured recipe. Secret values
// are replaced before this data leaves the core status layer.
func recipeStates(v *config.VM) ([]RecipeState, error) {
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		return nil, err
	}
	out := make([]RecipeState, 0, len(v.Recipes))
	for _, name := range v.Recipes {
		applied, done := v.Applied[name]
		state := RecipeState{
			Name: name, Applied: done, Version: applied.Version, At: applied.At,
			Health: applied.Health, Params: map[string]string{}, Outputs: map[string]string{},
		}
		if state.Health == "" {
			state.Health = string(HealthUnknown)
		}
		for key, value := range applied.Outputs {
			state.Outputs[key] = value
		}
		manifest, ok, manifestErr := recipes.ManifestFor(name)
		if manifestErr != nil || !ok {
			out = append(out, state)
			continue
		}
		for _, param := range manifest.SortedParams() {
			if param.Type == "secret" {
				state.SecretNames = append(state.SecretNames, param.Name)
				if secrets[name][param.Name] == "" {
					state.Params[param.Name] = SecretUnset
				} else {
					state.Params[param.Name] = SecretSet
				}
				continue
			}
			if value, given := v.Params[name][param.Name]; given {
				state.Params[param.Name] = value
			} else {
				state.Params[param.Name] = param.Default
			}
		}
		out = append(out, state)
	}
	return out, nil
}

// applied converts config.VM.Applied to core's own AppliedRecipe, so core.VM
// never carries a config type (see AppliedRecipe's doc comment). A nil input
// returns nil rather than an empty map, matching config.VM.Applied's own
// zero value for a vm.toml with no [applied] table.
func applied(m map[string]config.AppliedRecipe) map[string]AppliedRecipe {
	if m == nil {
		return nil
	}
	out := make(map[string]AppliedRecipe, len(m))
	for k, v := range m {
		outputs := make(map[string]string, len(v.Outputs))
		for name, value := range v.Outputs {
			outputs[name] = value
		}
		out[k] = AppliedRecipe{Version: v.Version, Hash: v.Hash, At: v.At, Health: v.Health, Outputs: outputs}
	}
	return out
}

// inferMissing fills v.OS and v.Backend from v.ISO's filename, in memory
// only. This covers a vm.toml written before those two fields existed; see
// iso.Infer's own doc comment. It is the same guess the create form makes
// for a BYO image, applied retroactively here.
//
// It fills only an EMPTY field. vm.toml is the user's stated intent; a
// filename guess never overrides it. The function never writes v.toml back
// out, so List/Get stay read-only. A vm.toml missing these fields keeps
// missing them on disk, and gets the same guess again on every load.
//
// Backend needs the same fill as OS, not a skip. backend.For(v) falls back
// to guest.Lookup(v.OS).Backend whenever v.Backend is empty, and that
// fallback is wrong for an entry like alpine-cloud, whose Backend
// (cloudinit) overrides its OS's default (apkovl). Filling OS but leaving
// Backend empty would make that fallback pick apkovl for a cloud-init
// image. iso.Infer reads Backend from the filename's extension, the same
// signal the catalog entries are built from, so it lands on cloudinit for
// alpine-cloud, matching the guess Create would have made.
//
// Unlike OS, iso.Infer's backend guess never comes back empty: an
// unrecognised name still resolves to "ssh". "ssh" and "" both hit
// backend.For's default case, so filling it changes nothing observable. It
// keeps this function's two return values symmetric.
//
// Only v.ISO is consulted, not v.Base, the cloud-mode overlay source. The
// pre-os-field vm.toml this handles is a live/disk case. A cloud VM missing
// OS/Backend is a separate, real gap, left for whoever hits it.
func inferMissing(v *config.VM) (osName, backend string) {
	osName, backend = v.OS, v.Backend
	if v.ISO == "" || (osName != "" && backend != "") {
		return osName, backend
	}
	guessedBackend, guessedOS := iso.Infer(filepath.Base(v.ISO))
	if osName == "" {
		osName = guessedOS
	}
	if backend == "" {
		backend = guessedBackend
	}
	return osName, backend
}

// List returns every VM in the data root, sorted by name, including broken
// ones as StateBroken rather than omitting them.
//
// config.List() silently drops any directory whose vm.toml fails to parse.
// config.ListBroken() recovers those, but as a second call a caller has to
// know to make. runLS (internal/cli) already merges both by hand. List does
// that merge once, here, so no future caller has to rediscover it or quietly
// drop real VMs.
func List() ([]VM, error) {
	cvms, err := config.List()
	if err != nil {
		return nil, err
	}
	var out []VM
	for _, cv := range cvms {
		if err := checkGuest(cv); err != nil {
			out = append(out, VM{Name: filepath.Base(cv.Dir), State: StateBroken, Error: err.Error()})
			continue
		}
		view, err := fromConfigChecked(cv)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}

	broken, err := config.ListBroken()
	if err != nil {
		return nil, err
	}
	for _, b := range broken {
		// A broken VM has no parsed struct to read fields from. It gets a
		// name, StateBroken, and the parse error, nothing else, matching
		// what runLS already renders as dashes.
		out = append(out, VM{Name: b.Name, State: StateBroken, Error: b.Err.Error()})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns the current view of one VM by name.
//
// A VM whose vm.toml is unparseable comes back as (VM{State: StateBroken},
// nil), not an error, the same reasoning as List: Destroy, or a TUI offering
// "delete" on a broken entry, needs to see and act on it. A bare error here
// would make it indistinguishable from "no such VM".
func Get(name string) (VM, error) {
	v, err := load(name)
	if errors.Is(err, ErrBroken) {
		return VM{Name: name, State: StateBroken, Error: err.Error()}, nil
	}
	if err != nil {
		return VM{}, err
	}
	return fromConfigChecked(v)
}

// Start launches VM name. It wraps qemu.Start; the actual work (pidfile,
// backend Prepare, marking a disk VM installed) lives there.
//
// qemu.Start already refuses to run twice, but with an untyped error
// ("%s is already running") that a caller can only detect by string
// matching. Start checks qemu.Running first and returns typed
// ErrAlreadyRunning instead; qemu.Start's own check never fires, because
// this function has already returned.
func Start(name string) error {
	v, err := load(name)
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, name)
	}
	return qemu.Start(v)
}

// EnsureRunning refuses with ErrNotRunning when v is not running. It exists
// so a caller above core, such as mcpsrv, answers the same error Stop and
// Exec give without importing qemu.Running itself.
func EnsureRunning(v *config.VM) error {
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, v.Name)
	}
	return nil
}

// Stop powers down VM name. qemu.Stop treats "already stopped" as a
// successful no-op; Stop does not. The CLI's `down` (internal/cli/cli.go's
// runDown) already refuses a stopped VM as a failure, and Stop preserves
// that behavior.
func Stop(name string) error {
	v, err := load(name)
	if err != nil {
		return err
	}
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, name)
	}
	return qemu.Stop(v)
}

// Destroy removes VM name's directory and vm.toml.
//
// Destroy refuses while running, matching both existing callers: runRM
// (internal/cli/cli.go) errors with "stop it first", and the TUI's delete
// key (internal/tui/list.go) refuses to offer delete at all. Stopping the
// VM first, as part of Destroy, would silently kill whatever process was
// running inside it, a second destructive action the caller did not ask
// for. A caller that wants that calls Stop then Destroy.
//
// config.VM.Delete already refuses to remove anything outside the data root
// (it checks filepath.Dir(v.Dir) == config.Root()); Destroy inherits that
// guard rather than re-implementing it.
func Destroy(name string) error {
	// The same data-root lock Create and Clone take. Clone's overlay
	// references its source's disk BY PATH, some time after Clone checks
	// that the source exists. Without this lock, a delete landing in that
	// window leaves the clone unable to open its backing file on first
	// start.
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	v, err := load(name)
	if errors.Is(err, ErrBroken) {
		// A broken VM's directory is still real and still deletable. This is
		// why Get/List surface broken VMs instead of hiding them: they can
		// be cleared instead of sitting forever unparseable.
		//
		// The running check below still applies. qemu.Running only needs
		// Dir, which is reconstructed here; it does not need a parsed
		// config.VM. Skipping this check let a vm.toml corrupted after its
		// VM was started bypass the running-VM refusal, deleting the
		// directory, pidfile, monitor socket and disk out from under a live
		// qemu process.
		bv := &config.VM{Name: name, Dir: filepath.Join(config.Root(), name)}
		if qemu.Running(bv) {
			return fmt.Errorf("%w: %s: stop it first", ErrAlreadyRunning, name)
		}
		return bv.Delete()
	}
	if err != nil {
		return err
	}
	if qemu.Running(v) {
		return fmt.Errorf("%w: %s: stop it first", ErrAlreadyRunning, name)
	}
	return v.Delete()
}
