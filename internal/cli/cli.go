// Package cli implements stoat's non-interactive, scriptable interface: a
// switch on the first argument dispatching into internal/core. No subcommand
// contains business logic of its own — parsing flags, printing, and the
// confirmation prompt are the whole of it, so the TUI, the CLI and an MCP
// server can never drift into bugs that reproduce in one and not the others.
//
// It no longer imports internal/qemu at all: deciding whether a VM is running,
// what happens when you start one, and whether a delete is allowed are core's
// answers now, not three front ends' separate ones. What remains outside core
// here is deliberate — internal/sshx for `ssh`, which execs and replaces this
// process, and internal/recipes/logx for the two commands that are genuinely
// about files on the host rather than about a VM.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/sshx"
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

	// Sub is the second word for subcommands that have one ("recipe list").
	// OS and Backend belong to "recipe new"; VM carries the recipe name
	// there, since it is the same "one positional argument" slot.
	Sub     string
	OS      string
	Backend string

	// Tag, Restore and Delete belong to "snapshot".
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
	// place — the CLI is a flag parser over core, not a second definition of
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
		// unparsed and then reject it as a stray argument — even though the
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
		// `stoat cp <src> <dst>`, where exactly one side is `<vm>:<path>` —
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
		// `stoat forward <name> 8080:80 8443:443` — the docker/ssh spelling,
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
		// VM name is the guest's command, verbatim — running `stoat exec work
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
  -q, --quiet, --no-interactive   suppress progress chatter (results and errors still print)
  -y                               (rm only) skip the delete confirmation

exit codes: 0 success, 1 runtime failure, 2 usage error

bare "stoat" with no arguments launches the interactive TUI.`
}

// Main parses args and dispatches, returning the process exit code. version
// is the build's version string (for the `version` subcommand). stdin is
// used only by `rm`'s confirmation prompt.
func Main(args []string, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "stoat:", err)
		fmt.Fprintln(stderr, usage())
		return ExitUsage
	}

	switch a.Cmd {
	case "help":
		fmt.Fprintln(stdout, usage())
		return ExitOK
	case "version":
		fmt.Fprintln(stdout, "stoat", version)
		return ExitOK
	}

	// Every other subcommand touches the data root, so it must be
	// initialised exactly as tui.Run() initialises it — otherwise a
	// first-run CLI user ends up with a half-initialised ~/.stoat.
	if err := config.EnsureRoot(); err != nil {
		fmt.Fprintln(stderr, "stoat:", err)
		return ExitFail
	}
	if err := recipes.Install(); err != nil {
		fmt.Fprintln(stderr, "stoat:", err)
		return ExitFail
	}
	if err := keys.Ensure(); err != nil {
		fmt.Fprintln(stderr, "stoat:", err)
		return ExitFail
	}
	// ponytail: same as the TUI — an unopenable log degrades to io.Discard
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

// colorEnabled reports whether ANSI color may be used on stdout: honouring
// NO_COLOR and disabling automatically when stdout is not a terminal, so
// piped output (`stoat ls | awk ...`) never carries escape codes.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// colorState pads to width FIRST, then wraps in escapes: the codes are
// zero-width on screen but count toward %-8s, so colouring before padding
// silently eats 9 columns and skews every row after STATE.
func colorState(state string, width int) string {
	return colorize(fmt.Sprintf("%-*s", width, state), state)
}

func colorize(padded, state string) string {
	if !colorEnabled() {
		return padded
	}
	switch state {
	case "running":
		return "\x1b[32m" + padded + "\x1b[0m" // green
	case "broken":
		return "\x1b[31m" + padded + "\x1b[0m" // red
	default:
		return padded
	}
}

func oneLine(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
}

// runCreate is the whole of `stoat create`: hand the flags to core and print
// what came back. There was no create subcommand before core existed, because
// everything it needed lived inside the TUI's form.
func runCreate(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Create(a.Spec)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: create:", err)
		if errors.Is(err, core.ErrImageNotDownloaded) {
			fmt.Fprintln(stderr, "stoat: download it from the TUI's image picker first")
		}
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "created %s (%s, %s, ssh port %d)\n", v.Name, v.OS, v.Mode, v.SSHPort)
		fmt.Fprintf(stdout, "start it with: stoat up %s\n", v.Name)
	}
	return ExitOK
}

// parseForwards reads "8080:80" pairs, host port first — the spelling docker
// and ssh -L both use, so the ordering is the one a user already has in their
// fingers. Getting it backwards silently binds the wrong port, so the error
// names the whole offending argument rather than just complaining about a
// number.
func parseForwards(pairs []string) ([]core.PortForward, error) {
	var out []core.PortForward
	for _, p := range pairs {
		host, guest, ok := strings.Cut(p, ":")
		if !ok {
			return nil, fmt.Errorf("%q is not a HOST:GUEST port pair", p)
		}
		h, err := strconv.Atoi(strings.TrimSpace(host))
		if err != nil {
			return nil, fmt.Errorf("%q: host port %q is not a number", p, host)
		}
		g, err := strconv.Atoi(strings.TrimSpace(guest))
		if err != nil {
			return nil, fmt.Errorf("%q: guest port %q is not a number", p, guest)
		}
		out = append(out, core.PortForward{HostPort: h, GuestPort: g})
	}
	return out, nil
}

// runForward shows, sets, or clears a VM's port forwards. core.Forward reports
// whether they are live NOW; when they are not, saying so is the whole point —
// a user who declared a forward on a running VM and got silence would conclude
// it was working and spend the next ten minutes debugging the guest.
func runForward(a *Args, stdout, stderr io.Writer) int {
	if len(a.Forwards) == 0 && !a.Clear {
		v, err := core.Get(a.VM)
		if err != nil {
			fmt.Fprintln(stderr, "stoat: forward:", err)
			return ExitFail
		}
		if len(v.Forwards) == 0 {
			fmt.Fprintf(stdout, "%s has no port forwards\n", a.VM)
			return ExitOK
		}
		for _, f := range v.Forwards {
			fmt.Fprintf(stdout, "%d:%d\n", f.HostPort, f.GuestPort)
		}
		return ExitOK
	}

	active, err := core.Forward(a.VM, a.Forwards)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: forward:", err)
		return ExitFail
	}
	if a.Quiet {
		return ExitOK
	}
	switch {
	case a.Clear:
		fmt.Fprintf(stdout, "cleared %s's port forwards\n", a.VM)
	default:
		for _, f := range a.Forwards {
			fmt.Fprintf(stdout, "%d:%d\n", f.HostPort, f.GuestPort)
		}
	}
	if !active {
		fmt.Fprintf(stdout, "%s is running; this takes effect at next start\n", a.VM)
	}
	return ExitOK
}

// runSnapshot lists, saves, restores or deletes a snapshot. Restoring is the
// one genuinely destructive path here — everything since the snapshot is
// discarded with no second copy — so it says what it did rather than
// succeeding silently.
func runSnapshot(a *Args, stdout, stderr io.Writer) int {
	switch {
	case a.Restore:
		if err := core.Restore(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "%s restored to %s\n", a.VM, a.Tag)
		}
		return ExitOK
	case a.Delete:
		if err := core.DeleteSnapshot(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "deleted %s\n", a.Tag)
		}
		return ExitOK
	case a.Tag != "":
		if err := core.TakeSnapshot(a.VM, a.Tag); err != nil {
			fmt.Fprintln(stderr, "stoat: snapshot:", err)
			return ExitFail
		}
		if !a.Quiet {
			fmt.Fprintf(stdout, "saved %s\n", a.Tag)
		}
		return ExitOK
	}

	snaps, err := core.Snapshots(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: snapshot:", err)
		return ExitFail
	}
	if len(snaps) == 0 {
		fmt.Fprintf(stdout, "%s has no snapshots\n", a.VM)
		return ExitOK
	}
	fmt.Fprintf(stdout, "%-24s %-10s %-20s %s\n", "TAG", "SIZE", "CREATED", "RAM")
	for _, s := range snaps {
		ram := "no"
		if s.VMState {
			ram = "yes"
		}
		fmt.Fprintf(stdout, "%-24s %-10s %-20s %s\n", s.Tag, s.Size, s.Created, ram)
	}
	return ExitOK
}

// runCopy moves one file between host and guest. Direction was already
// decided by Parse, from which side carried the "<vm>:" prefix.
func runCopy(a *Args, stdout, stderr io.Writer) int {
	var err error
	if a.ToRemote {
		err = core.CopyTo(context.Background(), a.VM, a.Local, a.Remote)
	} else {
		err = core.CopyFrom(context.Background(), a.VM, a.Remote, a.Local)
	}
	if err != nil {
		fmt.Fprintln(stderr, "stoat: cp:", err)
		return ExitFail
	}
	if !a.Quiet {
		if a.ToRemote {
			fmt.Fprintf(stdout, "copied %s to %s:%s\n", a.Local, a.VM, a.Remote)
		} else {
			fmt.Fprintf(stdout, "copied %s:%s to %s\n", a.VM, a.Remote, a.Local)
		}
	}
	return ExitOK
}

// runExec runs a command in a guest and RETURNS THE GUEST'S EXIT CODE, the
// same way ssh itself does. That is what makes it scriptable — `stoat exec vm
// make test && deploy` has to mean what it looks like.
//
// The cost is that stoat's own exit codes and the guest's share one range: a
// guest command exiting 2 is indistinguishable from a stoat usage error. That
// is accepted rather than worked around, because every alternative is worse —
// remapping the guest's status silently lies about what the command did, and a
// dedicated flag for "really give me the real code" would just be a footgun
// with extra steps. ssh made the same trade for the same reason.
//
// Note stoat's OWN failures here (no such VM, not running) still exit 1, and
// print to stderr, so they are distinguishable from guest output.
func runExec(a *Args, stdout, stderr io.Writer) int {
	// No timeout: a guest command may legitimately run for hours, and ^C
	// already kills the ssh process. Bounding it here would mean inventing a
	// limit nobody asked for.
	res, err := core.Exec(context.Background(), a.VM, a.Command)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: exec:", err)
		return ExitFail
	}
	// Streamed through verbatim, on the matching stream, so a caller can pipe
	// stdout without stderr contaminating it.
	fmt.Fprint(stdout, res.Stdout)
	fmt.Fprint(stderr, res.Stderr)
	return res.ExitCode
}

func runLS(a *Args, stdout, stderr io.Writer) int {
	vms, err := core.List()
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ls:", err)
		return ExitFail
	}

	fmt.Fprintf(stdout, "%-15s %-5s %-8s %-5s %-6s %s\n", "NAME", "MODE", "STATE", "CPUS", "RAM", "SSH")
	// core.List() sorts every VM — broken ones included — together by name,
	// so a broken VM can interleave alphabetically with good ones. The
	// original two calls (config.List then config.ListBroken) printed every
	// good VM first and every broken one after; two passes over core's
	// single (already name-sorted) slice reproduce that same grouping
	// without asking core to special-case an ordering only this one caller
	// wants.
	for _, v := range vms {
		if v.State == core.StateBroken {
			continue
		}
		state := "stopped"
		if v.State == core.StateRunning {
			state = "running"
		}
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5d %-6d %d\n",
			v.Name, v.Mode, colorState(state, 8), v.CPUs, v.RAM, v.SSHPort)
	}
	// Broken VMs are real entries: hiding them is the bug that was already
	// reported once. They get dashes for the fields a broken vm.toml can't
	// supply, plus a one-line reason.
	for _, v := range vms {
		if v.State != core.StateBroken {
			continue
		}
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5s %-6s %-4s %s\n",
			v.Name, "-", colorState("broken", 8), "-", "-", "-", oneLine(v.Error))
	}
	return ExitOK
}

func runUp(a *Args, stdout, stderr io.Writer) int {
	// core.Get first, exactly where config.Load used to sit: a caller must
	// learn "no such VM" (or "broken") before any progress line prints, not
	// after — the same reason it existed here originally.
	v, err := core.Get(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: up:", err)
		return ExitFail
	}
	// core.Get reports a broken VM as StateBroken rather than an error, so it
	// can be listed and deleted. Neither is true of starting one: refuse here,
	// before any output, rather than printing "starting x..." and only then
	// failing — a progress line that announces something the next line admits
	// is impossible is worse than no line at all.
	if v.State == core.StateBroken {
		fmt.Fprintln(stderr, "stoat: up:", v.Error)
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "starting %s...\n", a.VM)
	}
	if err := core.Start(a.VM); err != nil {
		fmt.Fprintln(stderr, "stoat: up:", err)
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s started (ssh :%d)\n", a.VM, v.SSHPort)
	return ExitOK
}

func runDown(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: down:", err)
		return ExitFail
	}
	// Same reasoning as up: "not running" is true of a broken VM but says
	// nothing useful, and the parse error is the only thing that helps.
	if v.State == core.StateBroken {
		fmt.Fprintln(stderr, "stoat: down:", v.Error)
		return ExitFail
	}
	if v.State != core.StateRunning {
		fmt.Fprintf(stderr, "stoat: down: %s is not running\n", a.VM)
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "stopping %s...\n", a.VM)
	}
	if err := core.Stop(a.VM); err != nil {
		// The State check above already catches the common case before any
		// output prints; errors.Is here is the defensive/authoritative path
		// (a VM stopped between the check and this call) rather than the
		// primary one, and it's what actually produces today's exact message
		// for that race.
		if errors.Is(err, core.ErrNotRunning) {
			fmt.Fprintf(stderr, "stoat: down: %s is not running\n", a.VM)
		} else {
			fmt.Fprintln(stderr, "stoat: down:", err)
		}
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s stopped\n", a.VM)
	return ExitOK
}

// runSSH replaces this process with ssh via syscall.Exec, so signals and the
// terminal behave exactly as a direct `ssh` invocation would, and stoat
// leaves no supervisor process behind.
func runSSH(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	path, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	argv := append([]string{"ssh"}, sshx.Args(v)...)
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		fmt.Fprintln(stderr, "stoat: ssh:", err)
		return ExitFail
	}
	return ExitOK // unreachable on success: the process image is gone
}

// runProvision runs sshx.Provision (which does the actual work and writes
// last-provision.log) in the background while polling that same file and
// copying new bytes to stdout, so the CLI shows live output without any
// duplicated provisioning logic.
func runProvision(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: provision:", err)
		return ExitFail
	}
	if v.Mode == "cloud" {
		// cloud-init's packages: list only runs at first boot, baked into
		// the seed when the overlay was created — there is nothing left for
		// ssh-based provisioning to do, and piping a recipe (which for cloud
		// VMs is #cloud-config YAML, not a shell script) into `sh -s` would
		// just fail.
		fmt.Fprintf(stdout, "%s is a cloud VM — recipes are applied automatically via cloud-init at first boot; recreate the VM to change them.\n", a.VM)
		return ExitOK
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "provisioning %s...\n", a.VM)
	}

	logPath := filepath.Join(v.Dir, "last-provision.log")
	done := make(chan error, 1)
	go func() { done <- sshx.Provision(v) }()

	if perr := streamFile(logPath, stdout, done); perr != nil {
		fmt.Fprintln(stderr, "stoat: provision:", perr)
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s provisioned\n", a.VM)
	return ExitOK
}

// streamFile copies newly-appended bytes of path to out every tick until
// done fires, then does one final copy so nothing written just before
// completion is missed.
func streamFile(path string, out io.Writer, done <-chan error) error {
	var offset int64
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			offset = copyNew(path, out, offset)
			return err
		case <-ticker.C:
			offset = copyNew(path, out, offset)
		}
	}
}

func copyNew(path string, out io.Writer, offset int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() <= offset {
		return offset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	io.Copy(out, f)
	return fi.Size()
}

func runRM(a *Args, stdin io.Reader, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: rm:", err)
		return ExitFail
	}
	if v.State == core.StateRunning {
		fmt.Fprintf(stderr, "stoat: rm: %s is running; stop it first\n", a.VM)
		return ExitFail
	}
	if !a.Yes {
		if a.Quiet {
			fmt.Fprintln(stderr, "stoat: rm: refusing to delete without -y in non-interactive mode")
			return ExitFail
		}
		fmt.Fprintf(stdout, "delete VM %s? [y/N] ", a.VM)
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "y" {
			fmt.Fprintln(stdout, "aborted")
			return ExitFail
		}
	}
	if err := core.Destroy(a.VM); err != nil {
		// Same belt-and-braces as runDown: the State check above already
		// refuses a running VM before the confirmation prompt even shows,
		// so this only fires on the started-after-the-check race — but it's
		// what reproduces today's exact "stop it first" message for it.
		if errors.Is(err, core.ErrAlreadyRunning) {
			fmt.Fprintf(stderr, "stoat: rm: %s is running; stop it first\n", a.VM)
		} else {
			fmt.Fprintln(stderr, "stoat: rm:", err)
		}
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s deleted\n", a.VM)
	return ExitOK
}

func runLogs(a *Args, stdout, stderr io.Writer) int {
	if err := logx.Init(); err != nil {
		fmt.Fprintln(stderr, "stoat: logs:", err)
		return ExitFail
	}
	lines, err := tailLines(logx.Path(), a.N)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: logs:", err)
		return ExitFail
	}
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	return ExitOK
}

func tailLines(path string, n int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// runDoctor prints core.Doctor's findings. It checks strictly more than it
// used to: this was two ad-hoc probes (qemu.Preflight plus a bare ssh
// LookPath), while core.Doctor runs the same set the installer's pre-install
// checklist does — qemu-system-x86_64, qemu-img, ssh, xorriso and /dev/kvm —
// so `stoat doctor` and `just setup` can no longer disagree about whether the
// host is ready.
//
// It also prints the fix command when there is one. The installer has always
// told the user how to repair a failed check; the CLI made them guess.
func runDoctor(a *Args, stdout, stderr io.Writer) int {
	var failed []core.HostCheck
	for _, c := range core.Doctor() {
		if !c.OK {
			failed = append(failed, c)
		}
	}
	if len(failed) == 0 {
		fmt.Fprintln(stdout, "ok")
		return ExitOK
	}
	for _, c := range failed {
		fmt.Fprintf(stdout, "FAIL: %s: %s\n", c.Name, c.Detail)
		if len(c.Fix) > 0 {
			fmt.Fprintf(stdout, "      try: %s\n", strings.Join(c.Fix, " "))
		}
	}
	return ExitFail
}

// runRecipe implements "recipe list" and "recipe new". Authoring a recipe has
// always been "put a correctly named file in the recipes directory" — the
// only real problem was that nothing told you so, or what the name had to be.
func runRecipe(a *Args, stdout, stderr io.Writer) int {
	switch a.Sub {
	case "list":
		names, err := recipes.Installed()
		if err != nil {
			fmt.Fprintln(stderr, "stoat: recipe list:", err)
			return ExitFail
		}
		fmt.Fprintln(stdout, recipes.Dir())
		if len(names) == 0 {
			fmt.Fprintln(stdout, "  (none)")
			return ExitOK
		}
		for _, n := range names {
			fmt.Fprintln(stdout, "  "+n)
		}
		return ExitOK

	case "new":
		path, err := recipes.New(a.VM, a.OS, a.Backend)
		if err != nil {
			fmt.Fprintln(stderr, "stoat: recipe new:", err)
			return ExitFail
		}
		fmt.Fprintln(stdout, path)
		if !a.Quiet {
			fmt.Fprintln(stdout, "edit it, then pick it in the new-vm form for a matching vm")
		}
		return ExitOK
	}
	fmt.Fprintln(stderr, "stoat: recipe: unknown action", a.Sub)
	return ExitUsage
}
