// Package config owns vm.toml files under the stoat data root.
package config

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/tomlx"
)

// UnknownKeyWriter receives one line per unknown vm.toml key that Load
// tolerates. A test overrides it to capture the warning.
var UnknownKeyWriter io.Writer = os.Stderr

// PortForward is one user-declared host->guest TCP forward, additional to
// the SSHPort forward every VM already gets. Validation (range, collisions
// with other VMs' ports, privileged ports) lives in core.Forward, not here.
// This package is the on-disk shape only, matching how VM itself carries no
// validation of its own fields.
type PortForward struct {
	HostPort  int `toml:"hostport"`
	GuestPort int `toml:"guestport"`
}

// AppliedRecipe records when a recipe was applied, which version, and the
// hash of the script that ran. An entry saved before Hash existed decodes
// with an empty string, never equal to a current script's hash, so that
// recipe re-runs once and then carries a real hash from then on.
type AppliedRecipe struct {
	Version    string            `toml:"version"`
	Hash       string            `toml:"hash"`
	ScriptHash string            `toml:"script_hash"`
	At         time.Time         `toml:"at"`
	Outputs    map[string]string `toml:"outputs,omitempty" comment:"written by stoat; do not edit"`
	Health     string            `toml:"health"`
}

// Share is one 9p export a project VM gets, beyond the legacy single Share
// and the per-VM work scratch. Tag is qemu's mount_tag and must be unique
// within a VM; "host" and "work" are reserved by the two exports qemu.Args
// already attaches. stoat writes these from stoat.toml at create and at
// reconcile; a hand edit is overwritten by the next stoat up.
type Share struct {
	Tag   string `toml:"tag"`
	Host  string `toml:"host"`
	Guest string `toml:"guest"`
}

// VM is one virtual machine. vm.toml is authoritative; there is no cache.
type VM struct {
	Name      string                       `toml:"name"`
	Mode      string                       `toml:"mode"` // "live" | "disk" | "cloud"
	OS        string                       `toml:"os"`
	ISO       string                       `toml:"iso"` // relative to the data root
	RAM       int                          `toml:"ram"` // MB
	CPUs      int                          `toml:"cpus"`
	Disk      string                       `toml:"disk"`      // disk mode only, e.g. "8G"
	Installed bool                         `toml:"installed"` // disk mode only; flips boot order
	Share     string                       `toml:"share"`     // host dir exposed as /mnt/host
	SSHPort   int                          `toml:"sshport"`
	Recipes   []string                     `toml:"recipes,omitempty"`
	Params    map[string]map[string]string `toml:"params,omitempty" comment:"written by stoat; do not edit"`

	// Display is the user's screen preference: "" or "auto" (default),
	// "window", or "vnc". "auto" opens a real qemu window on a graphical
	// host, and falls back to VNC only when the host has no display server;
	// set "vnc" to opt out of a window on a graphical host. core.validateDisplay
	// is the single place that checks the value; empty means an old vm.toml
	// predates this field, so it must read the same as "auto".
	// qemu.DisplayKind is the rule that turns this into DisplayWindow or
	// DisplayVNC.
	Display string `toml:"display"`

	// Forwards are user-declared TCP ports forwarded from host to guest, in
	// addition to the SSHPort forward that always exists. internal/qemu.Args
	// renders them into qemu's -netdev hostfwd= clauses. Changes apply at
	// the next start only: qemu cannot hot-add a hostfwd rule to a running
	// user-mode netdev. Editing this field while the VM runs changes
	// vm.toml immediately but has no effect on the live process. See
	// core.Forward and core.ErrAppliesAtNextStart, which exist so a caller
	// cannot mistake "saved" for "live".
	Forwards []PortForward `toml:"forwards,omitempty"`

	// Backend is the provisioning backend: "apkovl" | "cloudinit" | "ssh".
	// Written by the form at creation time; dispatch elsewhere in stoat
	// keys off Mode, not this field.
	Backend string `toml:"backend"`
	// Base is the absolute path to the shared base image an overlay is
	// created from. Cloud mode only.
	Base string `toml:"base"`
	// SSHUser is the account used for SSH-based provisioning/access. Empty
	// means root; callers apply that default themselves rather than this
	// package writing "root" into every vm.toml.
	SSHUser string `toml:"sshuser"`

	// CPUModel and RequiredCPU describe the CPU contract selected by a
	// catalog image. Empty values preserve the argument vector of a legacy
	// VM; catalog-backed creation owns nonempty values.
	CPUModel    string `toml:"cpu_model,omitempty"`
	RequiredCPU string `toml:"required_cpu,omitempty"`

	// ConsolePassword is the password for SSHUser at the VM's graphical
	// console (VNC), not over ssh. Only the cloudinit backend writes it:
	// cloud images lock every account by default, and stoat's seed sets
	// ssh_pwauth: false, so without this the console login is unanswerable.
	// Empty means no console login. That is correct for a live Alpine VM,
	// whose root already logs in with no password.
	ConsolePassword string `toml:"console_password"`

	// AllowExec is a per-VM opt-out of exec/copy_to/copy_from. The MCP
	// server enforces it, not this package or core.Exec; see
	// core.Spec.AllowExec's doc comment. Absent from a vm.toml, it means
	// true, not Go's zero value. Load makes that hold: toml.Decode alone
	// cannot tell "false" from "not written".
	AllowExec bool `toml:"allow_exec"`

	// AgentAccess is what an MCP agent may do in this VM: none, observe,
	// manage or exec. AllowExec stays for CLI/core callers that already read
	// it; the MCP server gates on this field instead. Empty means no
	// vm.toml value was ever written; core.Create fills in "manage" for a
	// VM it creates.
	AgentAccess string `toml:"agent_access,omitempty"`

	// Applied tracks which recipes have been run on this VM, keyed by recipe name.
	Applied map[string]AppliedRecipe `toml:"applied,omitempty" comment:"written by stoat; do not edit"`

	// Project is the absolute directory of the stoat.toml that declared this
	// VM, empty for a VM created by stoat new. A VM whose project directory
	// is gone still lists and still runs; ls marks the path "(missing)".
	Project string `toml:"project"`

	// Shares are the project's 9p exports, mounted under /work in the guest.
	// The legacy single Share field stays as it is: it is a different export
	// (read-only, at /mnt/host) with its own users.
	Shares []Share `toml:"shares,omitempty"`

	Dir string `toml:"-"` // absolute path to the VM directory
}

// Param reads one stored recipe parameter.
func (v *VM) Param(recipe, name string) (string, bool) {
	if v == nil {
		return "", false
	}
	values, ok := v.Params[recipe]
	if !ok {
		return "", false
	}
	value, ok := values[name]
	return value, ok
}

// SetParam stores one non-secret recipe parameter.
func (v *VM) SetParam(recipe, name, value string) {
	if v.Params == nil {
		v.Params = make(map[string]map[string]string)
	}
	if v.Params[recipe] == nil {
		v.Params[recipe] = make(map[string]string)
	}
	v.Params[recipe][name] = value
}

// UnsetParam removes one stored recipe parameter.
func (v *VM) UnsetParam(recipe, name string) {
	if v == nil || v.Params == nil {
		return
	}
	values, ok := v.Params[recipe]
	if !ok {
		return
	}
	delete(values, name)
	if len(values) == 0 {
		delete(v.Params, recipe)
	}
	if len(v.Params) == 0 {
		v.Params = nil
	}
}

// Root is the data root: $STOAT_HOME, or ~/.stoat.
func Root() string {
	if r := os.Getenv("STOAT_HOME"); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".stoat"
	}
	return filepath.Join(home, ".stoat")
}

// EnsureRoot creates the data root and its fixed subdirectories.
func EnsureRoot() error {
	for _, d := range []string{"isos", "recipes"} {
		if err := os.MkdirAll(filepath.Join(Root(), d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// reserved reports whether a directory in the data root is stoat's own rather
// than a VM. Everything not listed here is treated as a VM directory, so a new
// one must be added or it gets scanned as a candidate VM. Index refreshes use
// the two hidden workspace prefixes below; their contents can include vm.toml
// files copied from a previously published index.
func reserved(name string) bool {
	switch name {
	case "isos", "recipes", "shared", "logs", "index":
		return true
	}
	return strings.HasPrefix(name, ".stoat-index-stage-") ||
		strings.HasPrefix(name, ".stoat-index-old-")
}

// Expand resolves a leading ~ against the user's home directory. Exported so
// callers outside this package (internal/cli's `cp`) resolve a host path the
// same way vm.toml's own Share field does, rather than growing a second
// implementation of the same rule.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func (v *VM) path() string           { return filepath.Join(v.Dir, "vm.toml") }
func (v *VM) DiskPath() string       { return filepath.Join(v.Dir, "disk.qcow2") }
func (v *VM) PidPath() string        { return filepath.Join(v.Dir, "qemu.pid") }
func (v *VM) MonitorPath() string    { return filepath.Join(v.Dir, "monitor.sock") }
func (v *VM) QMPPath() string        { return filepath.Join(v.Dir, "qmp.sock") }
func (v *VM) VNCPath() string        { return filepath.Join(v.Dir, "vnc.sock") }
func (v *VM) OvlDir() string         { return filepath.Join(v.Dir, "ovl") }
func (v *VM) ConsoleLogPath() string { return filepath.Join(v.Dir, "console.log") }

// WorkDir is the VM's writable 9p export, mounted at /mnt/work in the guest.
//
// It lives under the data root rather than inside v.Dir so a human has a
// clean place to browse (~/.stoat/shared/<vm>/) instead of digging through
// runtime state like qemu.pid and monitor.sock.
//
// Derived from v.Dir, not v.Name: the directory basename is the VM's
// identity everywhere else in stoat (see core.VM.Name), so a vm.toml whose
// name field disagrees with its directory can't point this at another VM's
// share.
func (v *VM) WorkDir() string { return filepath.Join(Root(), "shared", filepath.Base(v.Dir)) }

// ProvisionLogPath is the transcript of the most recent recipe run, written
// by sshx.Provision. Lives here rather than in sshx so every VM path has one
// home, matching ConsoleLogPath/DiskPath/etc.
func (v *VM) ProvisionLogPath() string { return filepath.Join(v.Dir, "last-provision.log") }

// ISOPath resolves the configured ISO against the data root.
func (v *VM) ISOPath() string {
	if filepath.IsAbs(v.ISO) {
		return v.ISO
	}
	return filepath.Join(Root(), v.ISO)
}

// Save writes vm.toml, creating the VM directory if needed.
func (v *VM) Save() error {
	if v.Dir == "" {
		v.Dir = filepath.Join(Root(), v.Name)
	}
	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		return err
	}
	return tomlx.Encode(v.path(), v)
}

// Load reads one VM by name.
func Load(name string) (*VM, error) {
	dir := filepath.Join(Root(), name)
	path := filepath.Join(dir, "vm.toml")
	// Absent allow_exec means true; the seed survives the decode, a written
	// false overrides it.
	v := &VM{AllowExec: true}
	defined, err := tomlx.DecodeDefined(path, v, []string{"allow_exec"}, tomlx.Warn(UnknownKeyWriter))
	if err != nil {
		return nil, err
	}
	v.Dir = dir
	v.Share = Expand(v.Share)
	if v.AgentAccess == "" {
		v.AgentAccess = legacyAgentAccess(defined[0], v.AllowExec)
	}
	return v, nil
}

// legacyAgentAccess maps a vm.toml written before agent_access existed to a
// level. v.AllowExec is already seeded true for an absent key (the comment
// above Load), so it alone cannot tell that case apart from an explicit
// `allow_exec = true`; only allowExecDefined tells them apart, and only the
// explicit case earns "exec". Everything else, including an absent key, is
// "manage".
func legacyAgentAccess(allowExecDefined, allowExec bool) string {
	if allowExecDefined && allowExec {
		return "exec"
	}
	return "manage"
}

// List returns every VM in the data root, sorted by name.
func List() ([]*VM, error) {
	entries, err := os.ReadDir(Root())
	if err != nil {
		return nil, err
	}
	var vms []*VM
	for _, e := range entries {
		if !e.IsDir() || reserved(e.Name()) {
			continue
		}
		v, err := Load(e.Name())
		if err != nil {
			continue // not a VM directory
		}
		vms = append(vms, v)
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
	return vms, nil
}

// Broken describes a VM directory whose vm.toml exists but failed to parse.
// A directory with no vm.toml at all is not a VM and is never reported here.
type Broken struct {
	Name string
	Err  error
}

// ListBroken returns every VM directory whose vm.toml exists but fails to
// parse, sorted by name. It is the counterpart to List: List silently omits
// these directories, since a broken vm.toml cannot yield a usable *VM. A
// caller that wants to tell the user "this VM is broken", rather than
// making it look deleted, must call ListBroken separately.
func ListBroken() ([]Broken, error) {
	entries, err := os.ReadDir(Root())
	if err != nil {
		return nil, err
	}
	var broken []Broken
	for _, e := range entries {
		if !e.IsDir() || reserved(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(Root(), e.Name(), "vm.toml")); err != nil {
			continue // no vm.toml: not a VM directory
		}
		if _, err := Load(e.Name()); err != nil {
			broken = append(broken, Broken{Name: e.Name(), Err: err})
		}
	}
	sort.Slice(broken, func(i, j int) bool { return broken[i].Name < broken[j].Name })
	return broken, nil
}

// sshPortLine is a best-effort match for a `sshport = N` line in a vm.toml
// that otherwise fails to parse (e.g. an unterminated string earlier in the
// file). Used by FreePort so a broken VM's port isn't handed out again.
var sshPortLine = regexp.MustCompile(`(?m)^\s*sshport\s*=\s*(\d+)\s*$`)

// Delete removes the VM directory. It never touches isos/.
func (v *VM) Delete() error {
	if v.Dir == "" || filepath.Dir(v.Dir) != Root() {
		return fmt.Errorf("refusing to delete %q: outside the data root", v.Dir)
	}
	// The 9p work share lives outside v.Dir, so removing the VM directory
	// alone leaks it. Guarded the same way: only ever under <root>/shared.
	if work := v.WorkDir(); filepath.Dir(work) == filepath.Join(Root(), "shared") {
		if err := os.RemoveAll(work); err != nil {
			return err
		}
	}
	return os.RemoveAll(v.Dir)
}

// BrokenSSHPort best-effort reads the sshport out of a VM directory whose
// vm.toml does not parse, so callers assigning a port can avoid one that a
// broken VM is still committed to. An error means no port could be read;
// that is not the same as the VM claiming none.
func BrokenSSHPort(name string) (int, error) {
	data, err := os.ReadFile(filepath.Join(Root(), name, "vm.toml"))
	if err != nil {
		return 0, err
	}
	m := sshPortLine.FindSubmatch(data)
	if m == nil {
		return 0, fmt.Errorf("%s: no sshport line", name)
	}
	return strconv.Atoi(string(m[1]))
}

// FreePort returns the first free TCP port at or above 2200 on loopback that
// is both bindable right now and not already claimed by an existing VM's
// vm.toml. A created-but-stopped VM holds nothing open, so the bindability
// check alone is not enough to avoid handing out the same port twice in a
// row.
func FreePort() (int, error) {
	claimed := map[int]bool{}
	if vms, err := List(); err == nil {
		// An unreadable/missing data root (fresh install) means no VMs are
		// claimed; List's error is deliberately ignored here.
		for _, v := range vms {
			claimed[v.SSHPort] = true
		}
	}
	// A broken vm.toml can't be parsed for its port, but that port is very
	// likely still committed to the VM's disk image, so best-effort regex
	// the raw file text for it too.
	if broken, err := ListBroken(); err == nil {
		for _, b := range broken {
			if p, err := BrokenSSHPort(b.Name); err == nil {
				claimed[p] = true
			}
		}
	}
	for p := 2200; p < 2300; p++ {
		if claimed[p] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		_ = l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no free port in 2200-2299")
}
