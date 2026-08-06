package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/qemu"
)

// State is what List/Get answer at the moment they are called: never cached,
// always re-derived from the process table and the filesystem, exactly like
// runLS does today.
//
// The design doc (docs/design/core-api.md §1) lists six states. Only three
// are knowable from what exists right now:
//
//   - StateStopped and StateRunning come straight from qemu.Running, which
//     already does the hard part (pid liveness plus a /proc/<pid>/cmdline
//     check, so a reused pid never reads as "running").
//   - StateBroken comes from a vm.toml that exists but fails to parse,
//     config.ListBroken's existing concept.
//
// StateStarting, StateApplying and StateFailed are NOT declared here. Each
// needs progress tracking that nothing in the codebase produces yet: no code
// path currently distinguishes "qemu process is up" from "guest is
// reachable", nothing records that recipes are mid-apply, and nothing
// records that the last operation on a VM failed. Declaring those constants
// now would give List/Get a return type that claims six states while only
// ever producing three: a caller that switches on State exhaustively would
// have three cases that can never trigger, which is worse than an enum that
// is honestly incomplete. Add them when Start/Apply gain the tracking to
// back them, not before.
type State string

const (
	StateStopped State = "stopped"
	StateRunning State = "running"
	StateBroken  State = "broken"
)

// ErrNotRunning is returned by Stop (and by Destroy, transitively) for a VM
// that is not currently running: the honest answer, distinct from silently
// succeeding. qemu.Stop itself treats "already stopped" as a no-op; Stop
// checks first so the caller gets a typed signal instead of a silent nothing.
var ErrNotRunning = errors.New("not running")

// ErrAlreadyRunning is returned by Start for a VM that is already running,
// and reused by Destroy for a VM that is still running: both are the same
// shape of refusal: "this operation needs the VM stopped, and it isn't".
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
// against a VM, and when. Defined here rather than re-exported from config,
// because core is the headless layer the CLI, the TUI and the MCP server all
// depend on, and a type alias would leak config as part of that contract.
type AppliedRecipe struct {
	Version string
	At      time.Time
}

// VM is the answer to "what is this VM doing right now", not the on-disk
// record. It is a deliberately separate type from config.VM rather than the
// same struct with a State bolted on:
//
//   - config.VM is vm.toml: what was asked for, valid the instant it was
//     last saved, and meaningless to compare against "is it running" without
//     also calling qemu.Running.
//   - core.VM is a point-in-time answer: vm.toml plus qemu.Running, computed
//     together so a caller never has one without the other and never has to
//     know that combining them is even a step.
//
// Fields present in the design doc's VM (§1) but not here: Progress (nothing
// produces one yet, see State's comment) and Created (nothing records a
// creation timestamp; vm.toml's mtime is not that, since Update or a
// recipe-list edit would move it).
type VM struct {
	// Name is the VM's DIRECTORY under the data root, which is its identity;
	// see the note above load(). It is NOT necessarily the `name` field in
	// vm.toml; when those diverge, this is the one that works.
	Name    string
	OS      string
	Mode    string // live | disk | cloud
	Backend string // apkovl | cloudinit | ssh
	State   State

	// StartedAt is the running QEMU process's start time (its pidfile's
	// mtime), zero when State is not StateRunning. A duration would freeze at
	// whatever it was when List ran: List is a snapshot and the TUI has no
	// periodic refresh, so a caller wanting "up 5m32s" recomputes it from
	// this with time.Since, which keeps ticking between reloads.
	StartedAt time.Time

	RAM     int
	CPUs    int
	Disk    string
	Share   string
	Recipes []string

	SSHPort int
	SSHUser string

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

	// Forwards are the user-declared host->guest port forwards. Carried here
	// rather than behind a separate accessor so there is exactly one way to
	// read a VM's state: a caller that has called Get has everything, and
	// cannot end up reasoning about forwards that are a round trip out of date
	// with the State next to them.
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

	Paths Paths

	// Error is populated only when State is StateBroken, and holds
	// config.Load's parse error so a caller can show the user why, not just
	// that it's broken.
	Error string
}

// A VM's IDENTITY is its DIRECTORY under the data root, never the `name` field
// inside its vm.toml. The two can diverge: editing a VM keeps its directory
// and can leave a different name in the file, and everything that acts on a
// VM is directory-anchored: config.Load builds Root()/<name>/vm.toml, qemu's
// pidfile and monitor socket come off v.Dir, and config.VM.Delete removes
// v.Dir. internal/tui's TestDeleteTargetsDirectoryNotName pins this for the
// delete path specifically, after it was got wrong once.
//
// So every `name string` parameter in this package is a DIRECTORY name, and
// VM.Name must report that same directory, otherwise List hands back an
// identifier that Get and Destroy reject, or worse, one that resolves to a
// different VM. That is not hypothetical: before this was fixed,
// List()[0].Name on a directory "work" holding `name = "work2"` returned
// "work2", and Get("work2") failed with ErrNotFound. Pinned by
// TestIdentityFromListRoundTrips.

// load resolves a VM DIRECTORY name to its config.VM, distinguishing the two
// ways that can fail: no such VM at all (ErrNotFound) versus a VM directory
// whose vm.toml exists but won't parse (ErrBroken). config.Load alone
// conflates these: both come back as a bare decode/stat error, which is
// exactly the distinction Start/Stop/Destroy need in order to give a broken VM
// a sensible error instead of a raw TOML parse message.
func load(name string) (*config.VM, error) {
	if _, err := os.Stat(filepath.Join(config.Root(), name, "vm.toml")); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	v, err := config.Load(name)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBroken, name, err)
	}
	return v, nil
}

// fromConfig builds the point-in-time view for a VM that parsed cleanly.
// State and Paths are the two things config.VM cannot answer for itself.
func fromConfig(v *config.VM) VM {
	state := StateStopped
	if qemu.Running(v) {
		state = StateRunning
	}
	osName, backend := inferMissing(v)
	return VM{
		// The DIRECTORY, not v.Name; see the identity note above load(). This
		// is the identifier every other operation in this package accepts, so
		// it is the only one List may hand out.
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
		ISO:             v.ISO,
		Base:            v.Base,
		ConsolePassword: v.ConsolePassword,
		Forwards:        v.Forwards,
		Installed:       v.Installed,
		AllowExec:       v.AllowExec,
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
		out[k] = AppliedRecipe{Version: v.Version, At: v.At}
	}
	return out
}

// inferMissing fills v.OS and v.Backend from v.ISO's filename, in memory
// only, for a vm.toml predating those two fields (see iso.Infer's own doc
// comment; this is the same guess the form makes for a BYO image at create
// time, just applied retroactively on load instead of never at all).
//
// Only an EMPTY field is filled: v.toml is the user's stated intent, and a
// filename guess never overrides it, even when they disagree. This never
// writes v.toml back out, so List/Get stay read-only; a v.toml missing these
// fields keeps missing them on disk forever, and gets the same guess again
// on every future load.
//
// Backend gets the same treatment as OS, not skipped: backend.For(v) falls
// back to guest.Lookup(v.OS).Backend whenever v.Backend is empty, and that
// fallback is WRONG for an entry like alpine-cloud, whose Backend
// (cloudinit) overrides its OS's usual one (apkovl, alpine's guest.Registry
// default). Filling OS but leaving Backend empty would make that fallback
// fire and pick apkovl for a cloud-init image. iso.Infer decides Backend
// from the filename's extension, the same signal the catalog entries
// themselves are built from, so it lands on cloudinit for alpine-cloud
// exactly like the guess Create would have made.
//
// Unlike OS, iso.Infer's backend guess never comes back empty (an
// unrecognised name still resolves to "ssh"). That is not "inventing a
// default" here: "ssh" and "" both hit backend.For's noop default case, so
// filling it in changes nothing observable, and doing so keeps this
// function's two return values symmetric instead of special-casing one.
//
// Only v.ISO is consulted, not v.Base (the cloud-mode overlay source): the
// pre-os-field vm.toml this exists for is the live/disk case in the bug
// report, and a cloud VM missing OS/Backend is a real but separate gap left
// for whoever hits it.
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
// This is the whole point of the operation, not an edge case bolted on:
// today config.List() silently drops any directory whose vm.toml fails to
// parse, and config.ListBroken() is a second, separate call a caller has to
// know exists in order to not lose track of that VM. runLS (internal/cli)
// already has to call both and merge them by hand; every future caller of
// this package would have had to rediscover that same requirement or quietly
// hide real VMs. List does the merge once, here.
func List() ([]VM, error) {
	cvms, err := config.List()
	if err != nil {
		return nil, err
	}
	var out []VM
	for _, cv := range cvms {
		out = append(out, fromConfig(cv))
	}

	broken, err := config.ListBroken()
	if err != nil {
		return nil, err
	}
	for _, b := range broken {
		// A broken VM can supply none of config.VM's fields: there is no
		// parsed struct to read them from, so it gets a name, StateBroken,
		// and the parse error, and nothing else. Matches what runLS already
		// renders as dashes.
		out = append(out, VM{Name: b.Name, State: StateBroken, Error: b.Err.Error()})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns the current view of one VM by name.
//
// A VM whose vm.toml is unparseable comes back as (VM{State: StateBroken},
// nil), not an error, the same reasoning as List: a caller (in particular
// Destroy, or a TUI wanting to offer "delete" on a broken entry) needs to be
// able to see and act on a broken VM, and returning a bare error here would
// make it indistinguishable from "no such VM".
func Get(name string) (VM, error) {
	v, err := load(name)
	if errors.Is(err, ErrBroken) {
		return VM{Name: name, State: StateBroken, Error: err.Error()}, nil
	}
	if err != nil {
		return VM{}, err
	}
	return fromConfig(v), nil
}

// Start launches VM name. It is a thin wrapper over qemu.Start: the actual
// work (pidfile, backend Prepare, marking a disk VM installed) all lives
// there and is not duplicated here.
//
// qemu.Start already refuses to run twice, but with an untyped error
// ("%s is already running") that a caller can only detect by string
// matching. Checking qemu.Running first, before calling it, is what turns
// that into ErrAlreadyRunning, not a second guard, just a typed one placed
// ahead of the one qemu.Start already has (which then never fires, because
// this function has already returned).
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

// Stop powers down VM name. qemu.Stop treats "already stopped" as a
// successful no-op; Stop does not: the CLI's `down` already refuses a
// stopped VM as a failure (internal/cli/cli.go's runDown), and that is the
// behaviour this preserves rather than regresses to a silent success.
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
// Decision: REFUSE while running, matching both existing callers exactly:
// runRM (internal/cli/cli.go) errors with "stop it first", and the TUI's
// delete key (internal/tui/list.go) shows a toast telling the user to stop
// it rather than offering to delete at all. Stopping-first was the other
// option the brief raised, but it would be a silent behaviour change: a
// caller asking to delete a VM would, without saying so, also kill whatever
// process was running inside it. Deleting is destructive enough already;
// bundling an implicit kill into it is a second destructive action a caller
// did not explicitly ask for. A caller that wants "stop it, then delete it"
// can call Stop then Destroy, two words, and it cannot get the order wrong.
//
// config.VM.Delete already refuses to remove anything outside the data root
// (it checks filepath.Dir(v.Dir) == config.Root()); that guard is untouched
// here, so Destroy inherits it rather than re-implementing it.
func Destroy(name string) error {
	// The same data-root lock Create and Clone take. Clone's overlay
	// references its source's disk BY PATH and is created some time after
	// Clone checks that source exists; without Destroy participating, a
	// delete landing in that window produces a clone that is created
	// successfully and then cannot open its backing file on first start. A
	// lock only one side takes closes nothing.
	unlock, err := config.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	v, err := load(name)
	if errors.Is(err, ErrBroken) {
		// A broken VM's directory is still real and still deletable; that is
		// precisely why Get/List surface broken VMs rather than hiding them,
		// so they CAN be cleared instead of sitting forever unparseable.
		//
		// It is still checked for a running process first. An earlier version
		// of this claimed Running() "would need a parsed config.VM it doesn't
		// have" and skipped the check; that was simply wrong: Running reads
		// v.Dir/qemu.pid and matches v.Dir against /proc/<pid>/cmdline, and
		// Dir is exactly what is reconstructed here. The bug it caused was
		// real: a vm.toml corrupted AFTER its VM was started made Destroy a
		// quiet backdoor around the refusal below, deleting the directory,
		// pidfile, monitor socket and disk, out from under a live qemu.
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
