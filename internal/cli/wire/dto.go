package wire

import (
	"encoding/base64"
	"unicode/utf8"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
)

// DTOs, not json tags on core types (§3.1). Reasons, in order of weight:
//
//  1. core.VM.Paths carries six absolute host paths and must never reach the
//     wire (§3.2). Struct tags would need a second type anyway to omit it
//     without also hiding it from every in-process (TUI) caller.
//  2. A field added to a core type must not silently change the wire
//     format: exposing one here is an edit to this file, a deliberate act
//     with a diff, while core is under concurrent development.
//  3. Go field names (CPUs, RAM, SSHPort) are the wrong public contract;
//     units belong in the name (ram_mb) for a reader that does arithmetic.
//  4. core.Snapshot.Size/Created are qemu table output scraped as strings,
//     not structured data; naming them size_display/created_display says so
//     instead of publishing free-form qemu output as a contract.
//
// Every slice-valued field is normalized nil -> []: Go's encoding/json
// emits "null" for a nil slice, and a Python `for f in vm["forwards"]`
// raises TypeError on that. nonNil below is the one place this is done, so
// every From* constructor below returning a slice-bearing struct routes
// through it.

// nonNil turns a nil slice into an empty, non-nil one so it marshals as []
// rather than null (§3.1, §6: "MAY rely on [] for an empty list, never
// null").
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// PortForward is core.PortForward (an alias for config.PortForward) with
// wire field names.
type PortForward struct {
	HostPort  int `json:"host_port"`
	GuestPort int `json:"guest_port"`
}

func FromPortForward(f core.PortForward) PortForward {
	return PortForward{HostPort: f.HostPort, GuestPort: f.GuestPort}
}

func FromPortForwards(fs []core.PortForward) []PortForward {
	out := make([]PortForward, len(fs))
	for i, f := range fs {
		out[i] = FromPortForward(f)
	}
	return nonNil(out)
}

// VM is core.VM for the wire (§3.2). Deliberately absent:
//
//   - Paths: six absolute host paths (core.VM.Paths); see this file's doc
//     comment. Not read anywhere in this constructor.
//   - ConsolePassword: core.VM's own doc comment says it must never reach a
//     wire format. Not read here either.
//   - ISO / Base: plain vm.toml facts a human debugging config might want,
//     but not part of the MCP boundary (§3.2). Judgment call: add them if a
//     real caller needs them.
//   - The VNC socket path, and any rendered command containing it. Display
//     below carries WHICH surface, never WHERE. A rendered attach command
//     still embeds the raw path, and an agent cannot run a GUI viewer, so
//     the path buys a consumer nothing. A human who needs it runs `stoat
//     get`.
//
// Error is present only for a broken VM (empty string omits it, matching
// every other VM where core.VM.Error is unset).
type VM struct {
	Name      string        `json:"name"`
	OS        string        `json:"os"`
	Mode      string        `json:"mode"`
	Backend   string        `json:"backend"`
	State     string        `json:"state"`
	CPUs      int           `json:"cpus"`
	RAMMB     int           `json:"ram_mb"`
	Disk      string        `json:"disk"`
	Share     string        `json:"share"`
	Recipes   []string      `json:"recipes"`
	SSHPort   int           `json:"ssh_port"`
	SSHUser   string        `json:"ssh_user"`
	Installed bool          `json:"installed"`
	Forwards  []PortForward `json:"forwards"`
	AllowExec bool          `json:"allow_exec"`
	// Display is "window" or "vnc" ("" on a broken VM, like every other field
	// a broken vm.toml cannot supply). Emitted rather than left for a
	// consumer to derive from mode and installed: a host with no graphical
	// session puts an uninstalled disk VM on VNC too, so that derivation is
	// already wrong.
	Display string `json:"display"`
	Error   string `json:"error,omitempty"`
}

// RecipeState is one recipe's redacted per-VM state.
type RecipeState struct {
	Name    string            `json:"name"`
	Applied bool              `json:"applied"`
	Version string            `json:"version"`
	At      string            `json:"at"`
	Health  string            `json:"health"`
	Params  map[string]string `json:"params"`
	Outputs map[string]string `json:"outputs"`
}

// VMStatus is the get/vm_status payload. RecipeStates is additive so the
// existing VM.recipes string list remains compatible with contract v2.
type VMStatus struct {
	VM
	Health       string        `json:"health"`
	RecipeStates []RecipeState `json:"recipes_detail"`
}

// FromVMStatus converts the stored VM status into its additive wire shape.
func FromVMStatus(v core.VM, graphical bool) VMStatus {
	return VMStatus{VM: FromVM(v, graphical), Health: string(v.Health), RecipeStates: []RecipeState{}}
}

// FromVM takes graphical (core.GraphicalSession) rather than calling it,
// keeping this constructor pure: FromVMs would otherwise re-answer a
// host-wide question once per VM in the list, and a test of this file would
// answer it differently depending on the machine it ran on.
func FromVM(v core.VM, graphical bool) VM {
	return VM{
		Name:      v.Name,
		OS:        v.OS,
		Mode:      v.Mode,
		Backend:   v.Backend,
		State:     string(v.State),
		CPUs:      v.CPUs,
		RAMMB:     v.RAM,
		Disk:      v.Disk,
		Share:     v.Share,
		Recipes:   nonNil(v.Recipes),
		SSHPort:   v.SSHPort,
		SSHUser:   v.SSHUser,
		Installed: v.Installed,
		Forwards:  FromPortForwards(v.Forwards),
		AllowExec: v.AllowExec,
		// DisplayKind, not DisplayFor: this constructor must not go looking at
		// PATH, which DisplayFor does once per VM it is handed.
		Display: core.DisplayKind(v, graphical),
		Error:   v.Error,
	}
}

func FromVMs(vs []core.VM, graphical bool) []VM {
	out := make([]VM, len(vs))
	for i, v := range vs {
		out[i] = FromVM(v, graphical)
	}
	return nonNil(out)
}

// Image is core.CatalogImage for the wire.
//
// BYO is derived, not carried by core.CatalogImage: the CLI infers "bring
// your own" from ID == "" and does not expose it as a field (§3.3). Computed
// once here and emitted explicitly, so a consumer never re-derives it.
type Image struct {
	ID         string `json:"id"`
	OS         string `json:"os"`
	Variant    string `json:"variant"`
	Backend    string `json:"backend"`
	File       string `json:"file"`
	Downloaded bool   `json:"downloaded"`
	Bytes      int64  `json:"bytes"`
	BytesExact bool   `json:"bytes_exact"`
	BYO        bool   `json:"byo"`
}

func FromCatalogImage(img core.CatalogImage) Image {
	return Image{
		ID:         img.ID,
		OS:         img.OS,
		Variant:    img.Variant,
		Backend:    img.Backend,
		File:       img.File,
		Downloaded: img.Downloaded,
		Bytes:      img.Bytes,
		BytesExact: img.Exact,
		BYO:        img.ID == "",
	}
}

func FromCatalogImages(imgs []core.CatalogImage) []Image {
	out := make([]Image, len(imgs))
	for i, img := range imgs {
		out[i] = FromCatalogImage(img)
	}
	return nonNil(out)
}

// DownloadResult is core.DownloadResult for the wire.
type DownloadResult struct {
	Path              string `json:"path"`
	Verified          bool   `json:"verified"`
	ChecksumAvailable bool   `json:"checksum_available"`
}

func FromDownloadResult(r core.DownloadResult) DownloadResult {
	return DownloadResult{
		Path:              r.Path,
		Verified:          r.Verified,
		ChecksumAvailable: r.ChecksumAvailable,
	}
}

// Snapshot is core.Snapshot for the wire. Size/Created are named
// *_display: they are qemu's own formatted table output (see
// core.Snapshot's doc comment), opaque and never to be parsed by a
// consumer (§6).
type Snapshot struct {
	Tag            string `json:"tag"`
	VMState        bool   `json:"vm_state"`
	SizeDisplay    string `json:"size_display"`
	CreatedDisplay string `json:"created_display"`
}

func FromSnapshot(s core.Snapshot) Snapshot {
	return Snapshot{
		Tag:            s.Tag,
		VMState:        s.VMState,
		SizeDisplay:    s.Size,
		CreatedDisplay: s.Created,
	}
}

func FromSnapshots(ss []core.Snapshot) []Snapshot {
	out := make([]Snapshot, len(ss))
	for i, s := range ss {
		out[i] = FromSnapshot(s)
	}
	return nonNil(out)
}

// HostCheck is core.HostCheck for the wire.
type HostCheck struct {
	Name   string   `json:"name"`
	OK     bool     `json:"ok"`
	Detail string   `json:"detail"`
	Fix    []string `json:"fix"`
}

func FromHostCheck(c core.HostCheck) HostCheck {
	return HostCheck{Name: c.Name, OK: c.OK, Detail: c.Detail, Fix: nonNil(c.Fix)}
}

func FromHostChecks(cs []core.HostCheck) []HostCheck {
	out := make([]HostCheck, len(cs))
	for i, c := range cs {
		out[i] = FromHostCheck(c)
	}
	return nonNil(out)
}

// PruneItem is core.PruneItem for the wire. Class is already the wire value
// on core.PruneItem itself, so this constructor translates nothing; it
// exists so a field added to core.PruneItem does not silently reach the
// wire unreviewed.
type PruneItem struct {
	Class string `json:"class"`
	Path  string `json:"path"`
}

func FromPruneItem(p core.PruneItem) PruneItem {
	return PruneItem{Class: p.Class, Path: p.Path}
}

func FromPruneItems(ps []core.PruneItem) []PruneItem {
	out := make([]PruneItem, len(ps))
	for i, p := range ps {
		out[i] = FromPruneItem(p)
	}
	return nonNil(out)
}

// Recipe is core.Recipe for the wire.
//
// reboot is the field a machine caller needs most: it decides whether "wait
// until reachable" after an apply answers about the guest before or after the
// restart.
type Recipe struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      int            `json:"schema"`
	Params      []RecipeParam  `json:"params"`
	Outputs     []RecipeOutput `json:"outputs"`
	Health      *RecipeHealth  `json:"health"`
	Reboot      bool           `json:"reboot"`
	Depends     []string       `json:"depends"`
	Runtime     string         `json:"runtime"`
}

// RecipeParam is one named recipe parameter in a machine-readable schema.
type RecipeParam struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
	Values   []string `json:"values"`
	Help     string   `json:"help"`
}

// RecipeOutput is one named recipe output in a machine-readable schema.
type RecipeOutput struct {
	Name string `json:"name"`
	Help string `json:"help"`
}

// RecipeHealth is a recipe's declared health check.
type RecipeHealth struct {
	Check   string `json:"check"`
	Timeout string `json:"timeout"`
}

// RecipeSchema is one recipe's machine-readable contract.
type RecipeSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      int            `json:"schema"`
	Runtime     string         `json:"runtime"`
	Reboot      bool           `json:"reboot"`
	Depends     []string       `json:"depends"`
	Params      []RecipeParam  `json:"params"`
	Outputs     []RecipeOutput `json:"outputs"`
	Health      *RecipeHealth  `json:"health"`
}

// FromRecipeSchema converts a core recipe contract to the named wire shape.
func FromRecipeSchema(core.Recipe) RecipeSchema { return RecipeSchema{} }

func FromRecipe(r core.Recipe) Recipe {
	return Recipe{
		Name:        r.Name,
		Description: r.Description,
		Schema:      r.Schema,
		Params:      []RecipeParam{},
		Outputs:     []RecipeOutput{},
		Health:      nil,
		Reboot:      r.Reboot,
		Depends:     nonNil(r.Depends),
		Runtime:     r.Runtime,
	}
}

func FromRecipes(rs []core.Recipe) []Recipe {
	out := make([]Recipe, len(rs))
	for i, r := range rs {
		out[i] = FromRecipe(r)
	}
	return nonNil(out)
}

// ApplyPlan is core.ApplyPlan for the wire: one recipe's entry in an
// `apply --dry-run`.
type ApplyPlan struct {
	Name    string `json:"name"`
	Action  string `json:"action"`
	Reason  string `json:"reason"`
	Version string `json:"version,omitempty"`
}

func FromApplyPlan(p core.ApplyPlan) ApplyPlan {
	return ApplyPlan{Name: p.Name, Action: p.Action, Reason: p.Reason, Version: p.Version}
}

func FromApplyPlans(ps []core.ApplyPlan) []ApplyPlan {
	out := make([]ApplyPlan, len(ps))
	for i, p := range ps {
		out[i] = FromApplyPlan(p)
	}
	return nonNil(out)
}

// RecipeIssue is core.RecipeIssue for the wire.
type RecipeIssue struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func FromRecipeIssue(i core.RecipeIssue) RecipeIssue {
	return RecipeIssue{Name: i.Name, Reason: i.Reason}
}

func FromRecipeIssues(is []core.RecipeIssue) []RecipeIssue {
	out := make([]RecipeIssue, len(is))
	for i, issue := range is {
		out[i] = FromRecipeIssue(issue)
	}
	return nonNil(out)
}

// ExecResult is core.ExecResult for the wire, plus non-UTF-8 guest output
// handling (§4). exec's stdout/stderr are arbitrary guest bytes.
// json.Marshal silently replaces invalid UTF-8 with U+FFFD, a lossy
// corruption. A stream that is not valid UTF-8 is carried instead as
// base64 in the matching *_base64 field, with the plain field omitted.
//
// Encoding is reported per stream, not as one flag for both: a consumer
// checking "is this printable" for stdout and stderr independently does not
// need a compound rule. An empty StdoutEncoding/StderrEncoding means "utf8".
type ExecResult struct {
	Stdout         string `json:"stdout,omitempty"`
	StdoutBase64   string `json:"stdout_base64,omitempty"`
	StdoutEncoding string `json:"stdout_encoding,omitempty"`
	Stderr         string `json:"stderr,omitempty"`
	StderrBase64   string `json:"stderr_base64,omitempty"`
	StderrEncoding string `json:"stderr_encoding,omitempty"`
	ExitCode       int    `json:"exit_code"`
}

func FromExecResult(r core.ExecResult) ExecResult {
	out := ExecResult{ExitCode: r.ExitCode}
	if utf8.ValidString(r.Stdout) {
		out.Stdout = r.Stdout
	} else {
		out.StdoutBase64 = base64.StdEncoding.EncodeToString([]byte(r.Stdout))
		out.StdoutEncoding = "base64"
	}
	if utf8.ValidString(r.Stderr) {
		out.Stderr = r.Stderr
	} else {
		out.StderrBase64 = base64.StdEncoding.EncodeToString([]byte(r.Stderr))
		out.StderrEncoding = "base64"
	}
	return out
}

// Guest is guest.OS for the wire.
type Guest struct {
	Name           string                    `json:"name"`
	Init           string                    `json:"init"`
	Shell          string                    `json:"shell"`
	Installer      string                    `json:"installer"`
	DefaultBackend string                    `json:"default_backend"`
	DefaultSSHUser string                    `json:"default_ssh_user"`
	Escalate       []string                  `json:"escalate"`
	Capabilities   []string                  `json:"capabilities"`
	Aliases        []string                  `json:"aliases"`
	FilenameHints  []string                  `json:"filename_hints"`
	SeedPackages   []string                  `json:"seed_packages"`
	Pkg            GuestPkg                  `json:"pkg"`
	Svc            map[string]string         `json:"svc"`
	Cmd            map[string]string         `json:"cmd"`
	Backend        map[string]map[string]any `json:"backend"`
	Source         string                    `json:"source"`
}

type GuestPkg struct {
	Setup           string            `json:"setup"`
	Install         []string          `json:"install"`
	Env             map[string]string `json:"env"`
	RuntimePackages map[string]string `json:"runtime_packages"`
}

func FromGuest(o guest.OS) Guest {
	nonNilMap := func(m map[string]string) map[string]string {
		if m == nil {
			return map[string]string{}
		}
		return m
	}
	backend := o.Backends
	if backend == nil {
		backend = map[string]map[string]any{}
	}
	return Guest{
		Name: o.Name, Init: string(o.Init), Shell: o.Shell, Installer: o.Installer,
		DefaultBackend: o.DefaultBackend, DefaultSSHUser: o.DefaultSSHUser,
		Escalate: nonNil(o.Escalate), Capabilities: nonNil(o.Capabilities),
		Aliases: nonNil(o.Aliases), FilenameHints: nonNil(o.FilenameHints),
		SeedPackages: nonNil(o.SeedPackages),
		Pkg:          GuestPkg{Setup: o.Pkg.Setup, Install: nonNil(o.Pkg.Install), Env: nonNilMap(o.Pkg.Env), RuntimePackages: nonNilMap(o.Pkg.RuntimePackages)},
		Svc:          map[string]string{"enable": o.Svc.Enable, "start": o.Svc.Start, "stop": o.Svc.Stop, "restart": o.Svc.Restart, "status": o.Svc.Status},
		Cmd:          nonNilMap(o.Cmd), Backend: backend, Source: o.Source,
	}
}

func FromGuests(os []guest.OS) []Guest {
	out := make([]Guest, len(os))
	for i, o := range os {
		out[i] = FromGuest(o)
	}
	return nonNil(out)
}

// Screenshot is `screenshot` data. Path is the host path qemu wrote, always
// absolute, so a caller that passed a relative -o can read back what
// actually happened.
type Screenshot struct {
	VM     string `json:"vm"`
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func FromShot(vm string, s core.Shot) Screenshot {
	return Screenshot{VM: vm, Path: s.Path, Bytes: s.Bytes, Width: s.Width, Height: s.Height}
}

// GuestList is `guest ls` data.
type GuestList struct {
	Guests []Guest `json:"guests"`
}

// GuestShow is `guest show` data.
type GuestShow struct {
	Guest Guest `json:"guest"`
}
