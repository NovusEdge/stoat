// Package cli implements stoat's non-interactive, scriptable interface: a
// switch on the parsed subcommand dispatching into internal/core. A
// subcommand only parses flags, prints, and runs the confirmation prompt.
// All business logic lives in core, so the TUI, the CLI and an MCP server
// share one set of bugs instead of three.
//
// This package does not import internal/qemu. Whether a VM is running, what
// happens on start, and whether a delete is allowed are core's decisions.
// Two things stay outside core on purpose: internal/sshx for `ssh`, which
// execs and replaces this process, and internal/recipes/logx for the two
// commands that operate on host files rather than a VM.
//
// The grammar lives in grammar.go (kong struct tags); this file holds Args,
// Parse's thin wrapper around kong, and Main's dispatch.
package cli

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/kong"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
)

// Exit codes, because the whole point of a CLI is scripting against them.
const (
	ExitOK    = 0
	ExitFail  = 1 // runtime failure: a stopped VM passed to `down`, ssh unreachable, ...
	ExitUsage = 2 // usage error: unknown subcommand, missing argument
)

// Args is the pure result of parsing argv. Parse never touches disk or the
// network, so it is testable without executing anything.
type Args struct {
	Cmd   string
	VM    string
	Quiet bool
	Yes   bool
	N     int // logs -n

	// Out belongs to "screenshot": the -o path, empty for the default.
	Out string

	// NoApply belongs to "up": it skips the automatic post-boot apply,
	// leaving `up` returning as soon as the VM starts, as it did before that
	// behavior existed.
	NoApply bool

	// DryRun belongs to "apply": it prints the plan (core.PlanApply) and runs
	// nothing.
	DryRun bool

	// JSON is set by Main from the pre-parse argv scan, never by Parse: the
	// flag has to be recognized before any parser exists so a usage error
	// can still produce an envelope. It implies Quiet, so every prose line
	// already gated on -q disappears without a second condition.
	JSON bool

	// Help carries kong's generated help text for the `help` subcommand and
	// for -h/--help. It replaces a hand-written usage() string that had to be
	// edited by hand every time a flag changed, and silently did not have to
	// be, which is how it came to document 10 of 26 subcommands.
	Help string

	// Sub is the second word for subcommands that have one ("recipe list").
	// OS and Backend belong to "recipe new"; VM carries the recipe name
	// there, since it is the same "one positional argument" slot.
	Sub     string
	OS      string
	Backend string

	// Prune belongs to "prune"; it is core's own options type for the same
	// reason Spec is: one place to add an option.
	Prune core.PruneOpts

	// Tag belongs to "snapshot" (the snapshot name) and to "clone" (the new
	// VM's name); Restore and Delete are snapshot's.
	Tag     string
	Restore bool
	Delete  bool

	// Local, Remote and ToRemote belong to "cp". The direction is resolved at
	// parse time from which argument carried the "<vm>:" prefix, so nothing
	// downstream has to re-derive it and get it backwards.
	Local    string
	Remote   string
	ToRemote bool

	// Forwards and Clear belong to "forward". Clear is separate from an empty
	// Forwards because they mean different things: no pairs means "show me",
	// --clear means "remove them all".
	Forwards []core.PortForward
	Clear    bool

	// Command belongs to "exec": the guest command as an argv, never a shell
	// string. core.Exec quotes each element for the guest's shell, so word
	// boundaries survive; flattening it to a string here would throw away the
	// very thing that makes that possible.
	Command []string

	// Spec belongs to "create". It is core's own type rather than a dozen
	// more fields here, so adding an option to a VM means adding it in one
	// place: the CLI is a flag parser over core, not a second definition of
	// what a VM is.
	Spec core.Spec

	// Until and Timeout belong to "wait"; Which belongs to "logs"; Only
	// belongs to "apply" and carries the names for "check-recipes".
	Until   core.Until
	Timeout time.Duration
	Which   core.Which
	Only    []string

	// Patch belongs to "update", and Changed names the flags that were
	// actually GIVEN. core.Patch is all pointers so "not set" differs from
	// "set to the zero value", and kong's own pointer fields carry that
	// distinction through without a second mechanism: an absent --share is a
	// nil pointer, and `--share ""` is a pointer to the empty string.
	Patch   core.Patch
	Changed []string
	Params  []ParamEdit
}

// ParamEdit is one recipe parameter edit parsed from create or update flags.
// Secret values are resolved at the run boundary, not while Parse interprets
// argv, so parsing remains free of prompts and environment reads.
type ParamEdit struct {
	Recipe string
	Param  string
	Value  string
	Secret bool
	Unset  bool
}

// usageError marks a Parse failure as an exit-2 condition. Every Parse
// failure is a usage error by construction: Parse never runs anything, so
// there is no other kind of error it could return.
type usageError string

func (e usageError) Error() string { return string(e) }

// newParser builds the kong parser. The two options are what keep kong from
// behaving like a program rather than a library: Exit swallows the os.Exit
// kong would otherwise call on -h and on a parse error, and Writers sends its
// help and error output into help rather than to the real stderr, so Main
// stays the only thing that decides what gets printed and with which exit
// code. Without both, --json could not answer a usage error in the contract.
func newParser(g *grammar, help *bytes.Buffer) (*kong.Kong, error) {
	return kong.New(g,
		kong.Name("stoat"),
		kong.Description("Run local QEMU VMs. No libvirt, no daemon.\n\n"+
			"Bare \"stoat\" with no arguments launches the interactive TUI.\n"+
			"Add --json to any command for one JSON object per line on stdout,\n"+
			"errors included; it implies --quiet and never prompts.\n\n"+
			"exit codes: 0 success, 1 runtime failure, 2 usage error"),
		// No UsageOnError: it only feeds FatalIfErrorf, which this package
		// never calls, so it would be configuration implying a behaviour that
		// does not happen. Main decides what gets printed.
		kong.Exit(func(int) {}),
		kong.Writers(help, help),
	)
}

// commandPath joins the selected command's COMMAND nodes, so a nested command
// comes back as "recipe new". ctx.Command() is deliberately not used: it
// interleaves positional placeholders ("exec <vm> <command>") and so cannot be
// switched on.
func commandPath(ctx *kong.Context) string {
	var parts []string
	for _, t := range ctx.Path {
		if t.Command != nil {
			parts = append(parts, t.Command.Name)
		}
	}
	return strings.Join(parts, " ")
}

// Parse turns argv (excluding the "stoat" program name) into an Args, or a
// usageError. It never executes anything, which is what makes it testable
// as a pure function: kong.Parse populates the grammar and returns, and no
// Run() method exists for it to call.
func Parse(args []string) (*Args, error) {
	if len(args) > 0 && args[0] == "exec" {
		return parseExec(args[1:])
	}

	var g grammar
	var help bytes.Buffer

	p, err := newParser(&g, &help)
	if err != nil {
		// A malformed grammar is a programming error in grammar.go, not
		// anything the user typed. It cannot be reached from a test that only
		// varies argv, so it is reported rather than swallowed.
		return nil, usageError("cli grammar: " + err.Error())
	}

	ctx, perr := p.Parse(args)

	// kong writes help into the buffer for -h/--help before returning an
	// error for the missing command. A non-empty buffer with a failed parse
	// therefore means "help was asked for", which is a success, not a usage
	// error. Checked before perr for exactly that reason.
	if help.Len() > 0 {
		return &Args{Cmd: "help", Help: help.String()}, nil
	}
	if perr != nil {
		return nil, usageError(perr.Error())
	}
	return g.toArgs(commandPath(ctx))
}

// parseExec handles `exec <vm> <cmd>...` without kong. Kong's passthrough is
// not verbatim enough for this command:
//
//   - A leading flag token resolves against the root's own flags first, so
//     `exec work -q` loses the -q to stoat's --quiet and the guest gets an
//     empty command.
//   - A leading shorthand cluster is split, so `exec work -la` reaches the
//     guest as "-l" "a".
//
// Both silently corrupt the guest command, which this command's contract
// requires to arrive untouched. wire.SplitJSONFlag stops its own argv scan
// at exec's VM name for the same reason.
func parseExec(rest []string) (*Args, error) {
	if len(rest) == 0 {
		return nil, usageError("exec: missing vm name")
	}
	name, cmd := rest[0], rest[1:]
	// A leading "--" is accepted and dropped for anyone in the habit of
	// writing it, but it is not required.
	if len(cmd) > 0 && cmd[0] == "--" {
		cmd = cmd[1:]
	}
	if len(cmd) == 0 {
		return nil, usageError("exec: missing command")
	}
	return &Args{Cmd: "exec", VM: name, Command: cmd}, nil
}

// helpText renders the same text `stoat --help` prints, for the `help`
// subcommand and for --json help. It is generated from the grammar, so unlike
// the usage() string it replaces, it cannot drift from the actual flags.
func helpText() string {
	var g grammar
	var help bytes.Buffer
	p, err := newParser(&g, &help)
	if err != nil {
		return "cli grammar: " + err.Error()
	}
	_, _ = p.Parse([]string{"--help"})
	return help.String()
}

// ok writes a command's terminal result. Under --json that is the single
// "result" line on stdout; otherwise it is nothing at all, and the caller's
// own text output stands. Every runX returns through here or through fail,
// so "exactly one terminal line" holds by construction.
func (a *Args) ok(stdout io.Writer, data any) int {
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultOK(a.Cmd, data)
	}
	return ExitOK
}

// fail reports err and returns ExitFail: one JSON result line on STDOUT under
// --json (§4: everything structured goes to stdout, errors included, because a
// consumer that has to merge two pipes to read one result will eventually
// interleave them wrong), the usual "stoat: <cmd>: <err>" on stderr otherwise.
func (a *Args) fail(stdout, stderr io.Writer, err error) int {
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.MapError(err))
		return ExitFail
	}
	fmt.Fprintf(stderr, "stoat: %s: %v\n", a.Cmd, err)
	return ExitFail
}

// failMsg is fail for a condition the CLI detects itself, where there is no
// core error to map. sentinel picks the code; msg is the human text, which
// stays byte-identical to what the command printed before --json existed.
func (a *Args) failMsg(stdout, stderr io.Writer, sentinel error, msg string) int {
	if a.JSON {
		_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.MapError(fmt.Errorf("%w: %s", sentinel, msg)))
		return ExitFail
	}
	fmt.Fprintf(stderr, "stoat: %s: %s\n", a.Cmd, msg)
	return ExitFail
}

// Main parses args and dispatches, returning the process exit code. version
// is the build's version string (for the `version` subcommand). stdin is
// used only by `rm`'s confirmation prompt.
func Main(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// The scan runs before Parse so --json is recognized even when parsing is
	// what failed: an unknown subcommand still answers in the contract.
	jsonMode, argv := wire.SplitJSONFlag(args)
	cmd := ""
	if len(argv) > 0 {
		cmd = argv[0]
	}
	if jsonMode {
		// §4's panic guarantee: a consumer sees either a terminal result line
		// or a dead process, never a silent exit with neither.
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(stderr, "panic: %v\n", r)
				_ = wire.NewEmitter(stdout).ResultErr(cmd, wire.InternalError(fmt.Sprintf("panic: %v", r)))
				code = ExitFail
			}
		}()
	}

	a, err := Parse(argv)
	if err != nil {
		if jsonMode {
			_ = wire.NewEmitter(stdout).ResultErr(cmd, wire.UsageError(err.Error()))
			return ExitUsage
		}
		fmt.Fprintln(stderr, "stoat:", err)
		fmt.Fprintln(stderr, helpText())
		return ExitUsage
	}
	a.JSON = jsonMode
	if jsonMode {
		// --json is non-interactive by definition, and -q under it is a no-op
		// rather than an error. Forcing Quiet here is what suppresses the prose
		// lines: they are all already gated on it.
		a.Quiet = true
	}
	if len(a.Params) > 0 {
		resolved, err := resolveParamEdits(a.Params, stdin, stderr, !jsonMode && streamIsTTY(stdin))
		if err != nil {
			return a.failMsg(stdout, stderr, core.ErrInvalidSpec, err.Error())
		}
		a.Params = resolved
	}

	switch a.Cmd {
	case "help":
		if a.JSON {
			return a.ok(stdout, map[string]any{"usage": a.Help})
		}
		fmt.Fprintln(stdout, a.Help)
		return ExitOK
	case "version":
		if a.JSON {
			return a.ok(stdout, map[string]any{"version": version, "contract": wire.ContractVersion})
		}
		fmt.Fprintln(stdout, "stoat", version)
		return ExitOK
	}

	// Every other subcommand touches the data root, so it must be
	// initialised exactly as tui.Run() initialises it, otherwise a
	// first-run CLI user ends up with a half-initialised ~/.stoat.
	for _, setup := range []func() error{config.EnsureRoot, recipes.Install, keys.Ensure} {
		if err := setup(); err != nil {
			if a.JSON {
				_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.MapError(err))
				return ExitFail
			}
			fmt.Fprintln(stderr, "stoat:", err)
			return ExitFail
		}
	}
	// ponytail: same as the TUI, an unopenable log degrades to io.Discard
	// rather than failing the command the user actually asked for. `logs`
	// re-Inits and reports its own error, since there the log IS the command.
	_ = logx.Init()
	defer func() { _ = logx.Close() }()
	logx.L().Debug("cli", "cmd", a.Cmd, "vm", a.VM)

	if err := guest.Load(filepath.Join(config.Root(), "guests")); err != nil {
		if a.JSON {
			_ = wire.NewEmitter(stdout).ResultErr(a.Cmd, wire.UsageError(err.Error()))
			return ExitUsage
		}
		fmt.Fprintln(stderr, "stoat:", err)
		return ExitUsage
	}

	switch a.Cmd {
	case "ls":
		return runLS(a, stdout, stderr)
	case "create":
		return runCreate(a, stdout, stderr)
	case "images":
		return runImages(a, stdout, stderr)
	case "pull":
		return runPull(a, stdout, stderr)
	case "clone":
		return runClone(a, stdout, stderr)
	case "prune":
		return runPrune(a, stdout, stderr)
	case "snapshot":
		return runSnapshot(a, stdout, stderr)
	case "cp":
		return runCopy(a, stdout, stderr)
	case "forward":
		return runForward(a, stdout, stderr)
	case "exec":
		return runExec(a, stdout, stderr)
	case "up":
		return runUp(a, stdout, stderr)
	case "down":
		return runDown(a, stdout, stderr)
	case "ssh":
		return runSSH(a, stdout, stderr)
	case "rm":
		return runRM(a, stdin, stdout, stderr)
	case "recipe":
		return runRecipe(a, stdout, stderr)
	case "guest":
		return runGuest(a, stdout, stderr)
	case "logs":
		return runLogs(a, stdout, stderr)
	case "screenshot":
		return runScreenshot(a, stdout, stderr)
	case "get":
		return runGet(a, stdout, stderr)
	case "ssh-command":
		return runSSHCommand(a, stdout, stderr)
	case "wait":
		return runWait(a, stdout, stderr)
	case "apply":
		return runApply(a, stdout, stderr)
	case "recipes":
		return runRecipes(a, stdout, stderr)
	case "check-recipes":
		return runCheckRecipes(a, stdout, stderr)
	case "update":
		return runUpdate(a, stdout, stderr)
	case "doctor":
		return runDoctor(a, stdout, stderr)
	default:
		// Unreachable: Parse already rejected anything not handled above.
		fmt.Fprintln(stderr, "stoat: unknown subcommand", a.Cmd)
		return ExitUsage
	}
}
