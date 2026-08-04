package wire

import (
	"encoding/base64"
	"unicode/utf8"

	"github.com/novusedge/stoat/internal/core"
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
//     comment. core.VM.Paths is not read anywhere in this constructor.
//   - ConsolePassword: core.VM's own doc comment says it must never reach a
//     wire format; not read here either.
//   - ISO / Base: plain vm.toml facts a human debugging config might want,
//     but absent from the design doc's own worked VM example (§3.2) and not
//     named among the fields the MCP boundary needs; omitted to match that
//     example exactly rather than guessed back in. Flagged as a judgment
//     call: add them if a real caller needs them.
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
	Error     string        `json:"error,omitempty"`
}

func FromVM(v core.VM) VM {
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
		Error:     v.Error,
	}
}

func FromVMs(vs []core.VM) []VM {
	out := make([]VM, len(vs))
	for i, v := range vs {
		out[i] = FromVM(v)
	}
	return nonNil(out)
}

// Image is core.CatalogImage for the wire.
//
// BYO is derived, not carried by core.CatalogImage: today's CLI infers
// "bring your own" from ID == "" and does not expose it as a field (§3.3
// call-out). Re-deriving that structural rule is exactly the kind of
// implicit contract the design doc warns against reimplementing wrong, so
// it is computed once here and emitted explicitly.
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

// PruneItem is core.PruneItem for the wire. Class is already the wire
// value on core.PruneItem itself (see that type's doc comment: it was
// designed with this contract in mind), so this constructor has nothing to
// translate; it exists for the same reason every other From* does, so a
// field added to core.PruneItem does not silently reach the wire unreviewed.
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
type Recipe struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	TargetOS string `json:"target_os"`
	Shared   bool   `json:"shared"`
}

func FromRecipe(r core.Recipe) Recipe {
	return Recipe{Name: r.Name, Label: r.Label, TargetOS: r.TargetOS, Shared: r.Shared}
}

func FromRecipes(rs []core.Recipe) []Recipe {
	out := make([]Recipe, len(rs))
	for i, r := range rs {
		out[i] = FromRecipe(r)
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

// ExecResult is core.ExecResult for the wire, plus the non-UTF-8 guest
// output handling (§4): exec's stdout/stderr are arbitrary guest bytes, and
// json.Marshal silently replaces invalid UTF-8 with U+FFFD, a lossy,
// silent corruption. When a stream is not valid UTF-8 it is carried instead
// as base64 in the matching *_base64 field, with that field's plain
// counterpart omitted.
//
// Encoding is reported per stream rather than as one flag for both (a
// judgment call: the design doc's own example shows one top-level
// "encoding" field and flags the choice as undecided). A consumer checking
// "is this printable" for stdout and stderr independently, which is the
// common case, does not need a compound rule to answer it: an empty
// StdoutEncoding/StderrEncoding means "utf8", matching the documented
// default in both cases.
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
