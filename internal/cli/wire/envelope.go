// Package wire is the JSON contract stoat's --json mode speaks (see
// docs/design/json-contract-draft.md). It exists because the MCP server is
// Python + fastmcp in a separate process and reaches internal/core only by
// running the stoat binary and reading its output: everything a Python
// caller would otherwise have to regex, guess at, or reconstruct is defined
// here instead.
//
// This package holds the envelope (§2), the error code table (§2), the DTOs
// (§3) and the argv scan (§1). It does not touch internal/cli/cli.go: wiring
// --json into Parse and the run* bodies is a later phase.
package wire

import (
	"encoding/json"
	"io"
)

// ContractVersion is "v" on every line (§6): an integer contract version,
// not the build version. It bumps only for a removal or a meaning change
// (a field deleted, a unit changed, an error code split or repurposed,
// "result" stopping being last); additions never bump it.
//
// v2: the recipe system moved from flat files whose OS was encoded in the
// filename to directories with a recipe.toml manifest, and Recipe lost
// label, target_os and shared. Three fields deleted is exactly the case this
// number exists for. There is no v1 compatibility path anywhere: a clean
// break, so nothing has to reason about which shape it is looking at.
// v3: recipe list changed shape. dir became roots, a list of {path, scope},
// and recipes became a list of RecipeEntry objects rather than names.
// allow_exec also becomes agent_access with four levels, and the MCP server
// moves into this binary, so a tool output is a wire DTO rather than a
// re-parse of a --json line.
const ContractVersion = 3

// Event types (§2, §4). "result" is the one terminal type; every other type
// is non-terminal and a consumer MUST ignore any type it does not recognize.
const (
	TypeResult   = "result"
	TypeProgress = "progress"
	TypeStage    = "stage"
	TypeLog      = "log"
)

// ErrorInfo is the envelope's "error" field (§2). Subject and Kind are
// optional and additive: core wraps its typed errors with the subject
// baked into the message text (not machine-extractable), so the CLI call
// site supplies them explicitly rather than this package trying to parse
// them back out.
type ErrorInfo struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Subject names what the error is about ("work", "os"); absent when
	// there is nothing more specific than Message to give.
	Subject string `json:"subject,omitempty"`
	// Kind classifies Subject: one of "vm", "field", "recipe", "image",
	// "snapshot", "port", "path", or absent.
	Kind string `json:"kind,omitempty"`
}

// WithSubject sets Subject and Kind and returns e, for a one-line call at
// the site that already knows what the error is about.
func (e *ErrorInfo) WithSubject(subject, kind string) *ErrorInfo {
	e.Subject = subject
	e.Kind = kind
	return e
}

// envelope is one line of JSON Lines output (§2). ok/data/error are only
// ever populated on a terminal "result" line: a non-terminal event (via
// Emitter.Event) carries only Data.
type envelope struct {
	V     int        `json:"v"`
	Type  string     `json:"type"`
	Cmd   string     `json:"cmd"`
	OK    *bool      `json:"ok,omitempty"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorInfo `json:"error,omitempty"`
}

// flusher is satisfied by *bufio.Writer and similar; Emitter flushes after
// every line when its underlying writer implements it, so a buffered stdout
// does not turn streaming into "silent until exit" (§4).
type flusher interface {
	Flush() error
}

// Emitter writes the JSON Lines stream: zero or more non-terminal events,
// then exactly one terminal Result, always last (§2, §4).
type Emitter struct {
	w   io.Writer
	enc *json.Encoder
}

// NewEmitter wraps w. SetEscapeHTML(false) matters here specifically:
// exec's guest output can contain "<", ">" and "&", and the default HTML
// escaping would silently rewrite bytes a consumer diffs against what the
// guest actually printed.
func NewEmitter(w io.Writer) *Emitter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Emitter{w: w, enc: enc}
}

// Event writes one non-terminal line: "progress", "stage" or "log".
func (e *Emitter) Event(typ, cmd string, data any) error {
	return e.write(envelope{V: ContractVersion, Type: typ, Cmd: cmd, Data: data})
}

// Result writes the terminal line. Exactly one of data/errInfo should be
// non-nil, matching the envelope's "data and error are mutually exclusive"
// rule (§2); ok is carried separately rather than derived from which one is
// set, since a consumer branching on "ok" and one branching on
// "\"error\" in obj" must never disagree.
func (e *Emitter) Result(cmd string, ok bool, data any, errInfo *ErrorInfo) error {
	return e.write(envelope{V: ContractVersion, Type: TypeResult, Cmd: cmd, OK: &ok, Data: data, Error: errInfo})
}

// ResultOK is Result(cmd, true, data, nil).
func (e *Emitter) ResultOK(cmd string, data any) error {
	return e.Result(cmd, true, data, nil)
}

// ResultErr is Result(cmd, false, nil, errInfo).
func (e *Emitter) ResultErr(cmd string, errInfo *ErrorInfo) error {
	return e.Result(cmd, false, nil, errInfo)
}

func (e *Emitter) write(env envelope) error {
	if err := e.enc.Encode(env); err != nil {
		return err
	}
	if f, ok := e.w.(flusher); ok {
		return f.Flush()
	}
	return nil
}

// SplitJSONFlag pre-scans argv for --json before flag parsing (§1), so a
// usage error (an unknown subcommand, a bad flag, before any FlagSet
// exists) can still produce an envelope.
//
// The hard cases are "exec" and the conventional positional terminator:
// exec parses no flags of its own (cli.go's runExec dispatch), because
// everything after the VM name is the guest's command, verbatim. The
// terminator likewise makes every following token positional, so a search for
// the literal "--json" must not lose that token while looking for stoat's
// global JSON flag. Thus "--json" is recognized anywhere in argv EXCEPT
// after exec's VM name or after "--":
//
//	stoat --json exec work ls -la   -> stoat
//	stoat exec --json work ls -la   -> stoat
//	stoat exec work ls --json       -> the guest, untouched
//
// argv here excludes the program name (argv[0], "stoat" itself), matching
// cli.Parse's own convention.
func SplitJSONFlag(argv []string) (jsonMode bool, rest []string) {
	// exec's own two positionals are argv[0] ("exec") and argv[1] (the VM
	// name); scanning stops consuming --json after that point, so anything
	// from argv[2] on is the guest's command and passes through untouched,
	// --json included. For every command, the first -- also ends this scan:
	// everything after it is positional data for the command.
	stop := len(argv)
	if len(argv) > 0 && argv[0] == "exec" {
		stop = 2
		if stop > len(argv) {
			stop = len(argv)
		}
	}

	rest = make([]string, 0, len(argv))
	for i, a := range argv {
		if i < stop && a == "--" {
			rest = append(rest, argv[i:]...)
			break
		}
		if i < stop && a == "--json" {
			jsonMode = true
			continue
		}
		rest = append(rest, a)
	}
	return jsonMode, rest
}
