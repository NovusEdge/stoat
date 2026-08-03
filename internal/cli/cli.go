// Package cli implements stoat's non-interactive, scriptable interface: a
// switch on the first argument dispatching to the same packages the TUI
// uses. No subcommand contains business logic of its own — each is a thin
// wrapper over internal/config, internal/qemu, internal/sshx, internal/keys,
// internal/recipes, and internal/logx, so the TUI and CLI can never drift
// into bugs that reproduce in one and not the other.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/logx"
	"github.com/novusedge/stoat/internal/qemu"
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
		fs := newFlagSet(cmd)
		var quiet, yes bool
		addQuiet(fs, &quiet)
		fs.BoolVar(&yes, "y", false, "skip the delete confirmation")
		if err := fs.Parse(rest); err != nil {
			return nil, usageError(err.Error())
		}
		if fs.NArg() < 1 {
			return nil, usageError("rm: missing vm name")
		}
		if fs.NArg() > 1 {
			return nil, usageError("rm: too many arguments")
		}
		return &Args{Cmd: "rm", VM: fs.Arg(0), Quiet: quiet, Yes: yes}, nil

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

func oneLine(err error) string {
	return strings.ReplaceAll(strings.TrimSpace(err.Error()), "\n", " ")
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

func runLS(a *Args, stdout, stderr io.Writer) int {
	vms, err := config.List()
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ls:", err)
		return ExitFail
	}
	broken, err := config.ListBroken()
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ls:", err)
		return ExitFail
	}

	fmt.Fprintf(stdout, "%-15s %-5s %-8s %-5s %-6s %s\n", "NAME", "MODE", "STATE", "CPUS", "RAM", "SSH")
	for _, v := range vms {
		state := "stopped"
		if qemu.Running(v) {
			state = "running"
		}
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5d %-6d %d\n",
			v.Name, v.Mode, colorState(state, 8), v.CPUs, v.RAM, v.SSHPort)
	}
	// Broken VMs are real entries: hiding them is the bug that was already
	// reported once. They get dashes for the fields a broken vm.toml can't
	// supply, plus a one-line reason.
	for _, b := range broken {
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5s %-6s %-4s %s\n",
			b.Name, "-", colorState("broken", 8), "-", "-", "-", oneLine(b.Err))
	}
	return ExitOK
}

func runUp(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: up:", err)
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "starting %s...\n", a.VM)
	}
	if err := qemu.Start(v); err != nil {
		fmt.Fprintln(stderr, "stoat: up:", err)
		return ExitFail
	}
	fmt.Fprintf(stdout, "%s started (ssh :%d)\n", a.VM, v.SSHPort)
	return ExitOK
}

func runDown(a *Args, stdout, stderr io.Writer) int {
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: down:", err)
		return ExitFail
	}
	if !qemu.Running(v) {
		fmt.Fprintf(stderr, "stoat: down: %s is not running\n", a.VM)
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "stopping %s...\n", a.VM)
	}
	if err := qemu.Stop(v); err != nil {
		fmt.Fprintln(stderr, "stoat: down:", err)
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
	v, err := config.Load(a.VM)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: rm:", err)
		return ExitFail
	}
	if qemu.Running(v) {
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
	if err := v.Delete(); err != nil {
		fmt.Fprintln(stderr, "stoat: rm:", err)
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

func runDoctor(a *Args, stdout, stderr io.Writer) int {
	var issues []string
	if err := qemu.Preflight(); err != nil {
		issues = append(issues, err.Error())
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		issues = append(issues, "ssh not found in PATH")
	}
	if len(issues) == 0 {
		fmt.Fprintln(stdout, "ok")
		return ExitOK
	}
	for _, i := range issues {
		fmt.Fprintln(stdout, "FAIL:", i)
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
