// Package cli implements stoat's non-interactive, scriptable interface: a
// switch on the first argument dispatching into internal/core. No subcommand
// contains business logic of its own: parsing flags, printing, and the
// confirmation prompt are the whole of it, so the TUI, the CLI and an MCP
// server can never drift into bugs that reproduce in one and not the others.
//
// It no longer imports internal/qemu at all: deciding whether a VM is running,
// what happens when you start one, and whether a delete is allowed are core's
// answers now, not three front ends' separate ones. What remains outside core
// here is deliberate: internal/sshx for `ssh`, which execs and replaces this
// process, and internal/recipes/logx for the two commands that are genuinely
// about files on the host rather than about a VM.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
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

	// JSON is set by Main from the pre-parse argv scan, never by Parse: the
	// flag has to be recognized before any FlagSet exists so a usage error
	// can still produce an envelope. It implies Quiet, so every prose line
	// already gated on -q disappears without a second condition.
	JSON bool

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
}

// usageError marks a Parse failure as an exit-2 condition. Every Parse
// failure is a usage error by construction: Parse never runs anything, so
// there is no other kind of error it could return.
type usageError string

func (e usageError) Error() string { return string(e) }

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Main reports errors itself, once, consistently
	return fs
}

// addQuiet registers -q, --quiet and --no-interactive as aliases for the
// same bool, per the brief: the user named --no-interactive specifically.
func addQuiet(fs *flag.FlagSet, quiet *bool) {
	fs.BoolVar(quiet, "q", false, "suppress progress chatter")
	fs.BoolVar(quiet, "quiet", false, "suppress progress chatter")
	fs.BoolVar(quiet, "no-interactive", false, "suppress progress chatter (alias for -q)")
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

// Parse turns argv (excluding the "stoat" program name) into an Args, or a
// usageError. It never executes anything, which is what makes it testable
// as a pure function.
func Parse(args []string) (*Args, error) {
	if len(args) == 0 {
		return nil, usageError("no subcommand given")
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "ls", "doctor", "version", "help":
		fs := newFlagSet(cmd)
		var quiet bool
		addQuiet(fs, &quiet)
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("%s: unexpected argument %q", cmd, fs.Arg(0)))
		}
		return &Args{Cmd: cmd, Quiet: quiet}, nil

	case "up", "down", "ssh", "provision":
		fs := newFlagSet(cmd)
		var quiet bool
		addQuiet(fs, &quiet)
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() < 1 {
			return nil, usageError(fmt.Sprintf("%s: missing vm name", cmd))
		}
		if fs.NArg() > 1 {
			return nil, usageError(fmt.Sprintf("%s: too many arguments", cmd))
		}
		return &Args{Cmd: cmd, VM: fs.Arg(0), Quiet: quiet}, nil

	case "rm":
		// The name is pulled off BEFORE flag parsing when it comes first, the
		// same trick "create" and "recipe new" use. Go's flag package stops at
		// the first non-flag argument, so `stoat rm work -y` used to leave -y
		// unparsed and then reject it as a stray argument, even though the
		// usage text has always documented exactly that form. Both orders work
		// now; only `rm -y work` did before.
		rem := rest
		var name string
		if len(rem) > 0 && !strings.HasPrefix(rem[0], "-") {
			name, rem = rem[0], rem[1:]
		}

		fs := newFlagSet(cmd)
		var quiet, yes bool
		addQuiet(fs, &quiet)
		fs.BoolVar(&yes, "y", false, "skip the delete confirmation")
		if err := fs.Parse(rem); err != nil {
			return nil, usageError(err.Error())
		}
		if name == "" {
			if fs.NArg() < 1 {
				return nil, usageError("rm: missing vm name")
			}
			name = fs.Arg(0)
			if fs.NArg() > 1 {
				return nil, usageError("rm: too many arguments")
			}
		} else if fs.NArg() != 0 {
			return nil, usageError("rm: too many arguments")
		}
		return &Args{Cmd: "rm", VM: name, Quiet: quiet, Yes: yes}, nil

	case "create":
		// Same positional-before-flags rule as "recipe new" below.
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, usageError("create: missing vm name")
		}
		name, rem := rest[0], rest[1:]

		fs := newFlagSet(cmd)
		var quiet bool
		s := core.Spec{Name: name}
		fs.StringVar(&s.Image, "image", "", "catalog image id, or a path to your own image")
		fs.StringVar(&s.OS, "os", "", "override the guest OS inferred from a byo image's filename")
		fs.StringVar(&s.Backend, "backend", "", "override the backend inferred from a byo image's filename")
		fs.StringVar(&s.Mode, "mode", "", "live or disk (alpine iso only; every other image has one mode)")
		fs.IntVar(&s.RAM, "ram", 0, "memory in MB")
		fs.IntVar(&s.CPUs, "cpus", 0, "vcpu count")
		fs.StringVar(&s.Disk, "disk", "", "disk size, absolute only (8G, 512M)")
		fs.StringVar(&s.Share, "share", "", "host directory to expose in the guest")
		fs.StringVar(&s.ConsolePassword, "console-password", "", `console password; "random" generates one`)
		recipeList := fs.String("recipes", "", "comma-separated recipe names to record on the VM")
		addQuiet(fs, &quiet)
		if err := fs.Parse(rem); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("create: unexpected argument %q", fs.Arg(0)))
		}
		if s.Image == "" {
			return nil, usageError("create: --image is required")
		}
		for _, r := range strings.Split(*recipeList, ",") {
			if r = strings.TrimSpace(r); r != "" {
				s.Recipes = append(s.Recipes, r)
			}
		}
		return &Args{Cmd: "create", VM: name, Spec: s, Quiet: quiet}, nil

	case "images":
		fs := newFlagSet(cmd)
		var quiet bool
		addQuiet(fs, &quiet)
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("images: unexpected argument %q", fs.Arg(0)))
		}
		return &Args{Cmd: "images", Quiet: quiet}, nil

	case "pull":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, usageError("pull: missing image id (see `stoat images`)")
		}
		id, rem := rest[0], rest[1:]
		fs := newFlagSet(cmd)
		var quiet bool
		addQuiet(fs, &quiet)
		if err := fs.Parse(rem); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("pull: unexpected argument %q", fs.Arg(0)))
		}
		return &Args{Cmd: "pull", VM: id, Quiet: quiet}, nil

	case "clone":
		if len(rest) != 2 {
			return nil, usageError("clone: need a source and a new name")
		}
		return &Args{Cmd: "clone", VM: rest[0], Tag: rest[1]}, nil

	case "prune":
		fs := newFlagSet(cmd)
		var quiet bool
		var opts core.PruneOpts
		// DryRun is the DEFAULT, inverted by --apply, because every other
		// spelling makes the destructive reading the easy one to type by
		// accident. Prune is the only command here that can remove several
		// unrelated things at once.
		var apply bool
		fs.BoolVar(&apply, "apply", false, "actually delete; without this, prune only reports")
		fs.BoolVar(&opts.Broken, "broken", false, "also remove VMs whose vm.toml will not parse")
		fs.BoolVar(&opts.Images, "images", false, "also remove downloaded images no VM refers to")
		addQuiet(fs, &quiet)
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("prune: unexpected argument %q", fs.Arg(0)))
		}
		opts.DryRun = !apply
		return &Args{Cmd: "prune", Prune: opts, Quiet: quiet}, nil

	case "snapshot":
		// `stoat snapshot <vm>` lists; `snapshot <vm> <tag>` saves;
		// `--restore` / `--delete` act on an existing tag. Listing is the
		// no-argument default for the same reason `forward` prints rather than
		// clears: the destructive readings of a half-typed command should not
		// be the ones that happen.
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, usageError("snapshot: missing vm name")
		}
		name, rem := rest[0], rest[1:]
		var tag string
		if len(rem) > 0 && !strings.HasPrefix(rem[0], "-") {
			tag, rem = rem[0], rem[1:]
		}

		fs := newFlagSet(cmd)
		var quiet, restore, del bool
		fs.BoolVar(&restore, "restore", false, "roll the VM back to <tag>, discarding everything since")
		fs.BoolVar(&del, "delete", false, "remove <tag>")
		addQuiet(fs, &quiet)
		if err := fs.Parse(rem); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("snapshot: unexpected argument %q", fs.Arg(0)))
		}
		if restore && del {
			return nil, usageError("snapshot: --restore and --delete are mutually exclusive")
		}
		if (restore || del) && tag == "" {
			return nil, usageError("snapshot: --restore and --delete need a tag")
		}
		return &Args{Cmd: "snapshot", VM: name, Tag: tag, Restore: restore, Delete: del, Quiet: quiet}, nil

	case "cp":
		// `stoat cp <src> <dst>`, where exactly one side is `<vm>:<path>`,
		// the scp/docker cp spelling. Direction is inferred from which side
		// carries the colon, so there is no --to/--from flag to get backwards.
		if len(rest) != 2 {
			return nil, usageError("cp: need a source and a destination, one of them <vm>:<path>")
		}
		srcVM, srcPath, srcRemote := strings.Cut(rest[0], ":")
		dstVM, dstPath, dstRemote := strings.Cut(rest[1], ":")
		switch {
		case srcRemote == dstRemote:
			// Both or neither: guest-to-guest is not something one scp
			// invocation can do across two forwarded ports, and host-to-host
			// is just `cp`.
			return nil, usageError("cp: exactly one side must be <vm>:<path>")
		case srcRemote:
			return &Args{Cmd: "cp", VM: srcVM, Remote: srcPath, Local: rest[1]}, nil
		default:
			return &Args{Cmd: "cp", VM: dstVM, Remote: dstPath, Local: rest[0], ToRemote: true}, nil
		}

	case "forward":
		// `stoat forward <name> 8080:80 8443:443`, the docker/ssh spelling,
		// host first. With no pairs it PRINTS the current forwards rather than
		// clearing them: silently wiping a VM's forwards because an argument
		// was forgotten is not a mistake worth allowing. Use --clear to mean it.
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return nil, usageError("forward: missing vm name")
		}
		name, rem := rest[0], rest[1:]

		fs := newFlagSet(cmd)
		var quiet, clear bool
		fs.BoolVar(&clear, "clear", false, "remove every forward from this VM")
		addQuiet(fs, &quiet)
		// Flags first so the pairs that follow are never eaten by flag parsing.
		var pairs []string
		for _, a := range rem {
			if strings.HasPrefix(a, "-") {
				if err := fs.Parse([]string{a}); err != nil {
					return nil, usageError(err.Error())
				}
				continue
			}
			pairs = append(pairs, a)
		}
		if clear && len(pairs) > 0 {
			return nil, usageError("forward: --clear takes no port pairs")
		}
		fwds, err := parseForwards(pairs)
		if err != nil {
			return nil, usageError("forward: " + err.Error())
		}
		return &Args{Cmd: "forward", VM: name, Forwards: fwds, Clear: clear, Quiet: quiet}, nil

	case "exec":
		// exec parses NO flags of its own, deliberately. Everything after the
		// VM name is the guest's command, verbatim: running `stoat exec work
		// ls -la` must send -la to ls, not have stoat try to interpret it.
		// A leading "--" is accepted and dropped for anyone in the habit of
		// writing it, but it is not required.
		if len(rest) == 0 {
			return nil, usageError("exec: missing vm name")
		}
		name, cmd := rest[0], rest[1:]
		if len(cmd) > 0 && cmd[0] == "--" {
			cmd = cmd[1:]
		}
		if len(cmd) == 0 {
			return nil, usageError("exec: missing command")
		}
		return &Args{Cmd: "exec", VM: name, Command: cmd}, nil

	case "recipe":
		// Positionals are pulled off BEFORE flag parsing: Go's flag package
		// stops at the first non-flag argument, so "recipe new x --os alpine"
		// would otherwise leave --os unparsed and reported as a stray
		// argument. Flags therefore come after the name, which is also how
		// the usage text spells it.
		if len(rest) == 0 {
			return nil, usageError(`recipe: expected "list" or "new <name>"`)
		}
		sub, rem := rest[0], rest[1:]
		var name string
		switch sub {
		case "list":
		case "new":
			if len(rem) == 0 || strings.HasPrefix(rem[0], "-") {
				return nil, usageError("recipe new: missing name")
			}
			name, rem = rem[0], rem[1:]
		default:
			return nil, usageError(fmt.Sprintf("recipe: unknown action %q (expected \"list\" or \"new\")", sub))
		}

		fs := newFlagSet(cmd)
		var quiet bool
		osName := fs.String("os", "", "target OS for a new shell recipe (alpine, ubuntu, debian, arch, fedora)")
		backend := fs.String("backend", "", `"cloudinit" for a cloud-init fragment; shell otherwise`)
		addQuiet(fs, &quiet)
		if err := fs.Parse(rem); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("recipe %s: unexpected argument %q", sub, fs.Arg(0)))
		}
		return &Args{Cmd: "recipe", Sub: sub, VM: name, OS: *osName, Backend: *backend, Quiet: quiet}, nil

	case "logs":
		fs := newFlagSet(cmd)
		var quiet bool
		n := fs.Int("n", 50, "number of lines to tail")
		addQuiet(fs, &quiet)
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() != 0 {
			return nil, usageError(fmt.Sprintf("logs: unexpected argument %q", fs.Arg(0)))
		}
		return &Args{Cmd: "logs", Quiet: quiet, N: *n}, nil

	default:
		return nil, usageError(fmt.Sprintf("unknown subcommand %q", cmd))
	}
}

func usage() string {
	return `usage: stoat <command> [flags]

commands:
  ls                   list VMs, one line per VM
  create <name> --image <id|path> [--ram MB] [--cpus N] [--disk 8G]
                [--share DIR] [--recipes a,b] [--mode live|disk]
                [--os NAME] [--backend NAME] [--console-password random]
                       create a VM without starting it

  up <name>            start a VM
  down <name>          stop a VM (graceful)
  ssh <name>           ssh into a VM, replacing this process
  images               list catalog and local images
  pull <image-id>      download a catalog image
  clone <vm> <newname>  copy a VM: overlay disk, fresh ssh port, no forwards
  prune [--apply] [--broken] [--images]
                       report (or with --apply, remove) stale partial
                       downloads; opt in to broken VMs and unused images
  snapshot <vm> [tag] [--restore|--delete]
                       list, save, restore or delete a snapshot
  cp <src> <dst>       copy a file in or out; one side is <vm>:<path>
  forward <name> [8080:80 ...] [--clear]
                       show, set or clear host:guest port forwards
  exec <name> <cmd>... run a command in a VM; exits with the GUEST's status.
                       Everything after <name> is the command, verbatim.
  provision <name>     run recipes, streaming output to stdout
  rm <name> [-y]       delete a VM; refuses while running, confirms unless -y
  logs [-n N]          tail the stoat log (default 50 lines)
  recipe list          list installed recipes and where they live
  recipe new <name> [--os alpine] [--backend cloudinit]
                       scaffold a recipe in the recipes directory
  doctor               check host prerequisites
  version              print the stoat version
  help                 show this message

global flags:
  --json                          one JSON object per line on stdout, errors included;
                                  implies --quiet and never prompts
  -q, --quiet, --no-interactive   suppress progress chatter (results and errors still print)
  -y                               (rm only) skip the delete confirmation

exit codes: 0 success, 1 runtime failure, 2 usage error

bare "stoat" with no arguments launches the interactive TUI.`
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
		fmt.Fprintln(stderr, usage())
		return ExitUsage
	}
	a.JSON = jsonMode
	if jsonMode {
		// --json is non-interactive by definition, and -q under it is a no-op
		// rather than an error. Forcing Quiet here is what suppresses the prose
		// lines: they are all already gated on it.
		a.Quiet = true
	}

	switch a.Cmd {
	case "help":
		if a.JSON {
			return a.ok(stdout, map[string]any{"usage": usage()})
		}
		fmt.Fprintln(stdout, usage())
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
	defer logx.Close()
	logx.L().Debug("cli", "cmd", a.Cmd, "vm", a.VM)

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
	case "provision":
		return runProvision(a, stdout, stderr)
	case "rm":
		return runRM(a, stdin, stdout, stderr)
	case "recipe":
		return runRecipe(a, stdout, stderr)
	case "logs":
		return runLogs(a, stdout, stderr)
	case "doctor":
		return runDoctor(a, stdout, stderr)
	default:
		// Unreachable: Parse already rejected anything not handled above.
		fmt.Fprintln(stderr, "stoat: unknown subcommand", a.Cmd)
		return ExitUsage
	}
}
