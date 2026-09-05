package cli

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/mcpsrv"
)

// The kong grammar: one struct per subcommand, tags instead of a hand-written
// FlagSet per case.
//
// Two tags here are load-bearing, confirmed against kong v1.16.0:
//
//   - ExecCmd.Command's `passthrough:""`. It must sit on the POSITIONAL, not
//     on the command: kong refuses a passthrough COMMAND that has more than
//     one positional (build.go's "must contain exactly one positional
//     argument"), and exec has two. On the positional it does what exec needs,
//     which is that `stoat exec work ls --json` sends "--json" to the guest.
//   - The *pointer fields on UpdateCmd. nil means the flag was absent, and a
//     flag given as the zero value still yields a non-nil pointer, so
//     `--share ""` clears a share while an omitted --share leaves it alone.
//     That distinction is the whole reason core.Patch is pointers, and getting
//     it wrong makes `update work --ram 8192` silently drop the share.
type grammar struct {
	// Quiet is on the root so every subcommand inherits it. --json is
	// deliberately absent: wire.SplitJSONFlag removes it from argv before kong
	// ever sees it, so that a usage error can still answer in the JSON
	// contract, and so exec's guest command keeps its own --json.
	Quiet bool `short:"q" name:"quiet" aliases:"no-interactive" help:"suppress progress chatter"`

	Init   initCmd   `cmd:"" help:"write a stoat.toml for this directory"`
	Status statusCmd `cmd:"" help:"one line per declared VM: state, health and drift"`

	LS      lsCmd      `cmd:"" name:"ls" help:"list VMs, one line per VM"`
	Get     getCmd     `cmd:"" help:"show one VM"`
	Create  createCmd  `cmd:"" help:"create a VM without starting it"`
	Update  updateCmd  `cmd:"" help:"change a stopped VM; only the flags you pass change"`
	Up      upCmd      `cmd:"" help:"start a VM"`
	Down    downCmd    `cmd:"" help:"stop a VM (graceful)"`
	Wait    waitCmd    `cmd:"" help:"block until a VM reaches a state"`
	RM      rmCmd      `cmd:"" name:"rm" help:"delete a VM; refuses while running"`
	Clone   cloneCmd   `cmd:"" help:"copy a VM: overlay disk, fresh ssh port, no forwards"`
	Exec    execCmd    `cmd:"" help:"run a command in a VM; exits with the GUEST's status"`
	SSH     sshCmd     `cmd:"" name:"ssh" help:"ssh into a VM, replacing this process"`
	SSHCmd  sshCmdCmd  `cmd:"" name:"ssh-command" help:"print the ssh argv instead of running it"`
	CP      cpCmd      `cmd:"" name:"cp" help:"copy a file in or out; one side is <vm>:<path>"`
	Forward forwardCmd `cmd:"" help:"show, set or clear host:guest port forwards"`

	Images   imagesCmd   `cmd:"" help:"list catalog and local images"`
	Pull     pullCmd     `cmd:"" help:"download a catalog image"`
	Snapshot snapshotCmd `cmd:"" help:"list, save, restore or delete a snapshot"`
	Prune    pruneCmd    `cmd:"" help:"report, or with --apply remove, stale files"`

	Apply        applyCmd        `cmd:"" aliases:"provision" help:"run the VM's recipes, streaming output"`
	Recipes      recipesCmd      `cmd:"" help:"list recipes, optionally only applicable ones"`
	CheckRecipes checkRecipesCmd `cmd:"" help:"report why a recipe would not apply"`
	Recipe       recipeCmd       `cmd:"" help:"author recipes"`
	Guest        recipeGuestCmd  `cmd:"" help:"list or show guest OS definitions"`

	Logs       logsCmd       `cmd:"" help:"tail a VM's log, or stoat's own"`
	Screenshot screenshotCmd `cmd:"" help:"write the VM's screen to a PNG"`
	Doctor     doctorCmd     `cmd:"" help:"check host prerequisites"`
	MCP        mcpCmd        `cmd:"" name:"mcp" help:"serve MCP, or configure a client to launch it"`
	Version    versionCmd    `cmd:"" help:"print the stoat version"`
	Help       helpCmd       `cmd:"" help:"show this message"`
}

// mcpCmd defaults to serve, because every MCP client launches the server as
// "stoat mcp" with no subcommand.
type mcpCmd struct {
	Serve   mcpServeCmd   `cmd:"" default:"withargs" help:"serve MCP over stdio"`
	Install mcpInstallCmd `cmd:"" help:"write an MCP client's config entry"`
	Doctor  mcpDoctorCmd  `cmd:"" help:"report contract, transport and client entries"`
}

type mcpServeCmd struct {
	HTTP      string  `name:"http" help:"serve streamable HTTP on this loopback address instead of stdio"`
	ToolBurst int     `name:"tool-burst" default:"30" help:"per-tool rate limit burst"`
	ToolRate  float64 `name:"tool-rate" default:"0.5" help:"per-tool refill, calls per second"`
	Burst     int     `name:"burst" default:"60" help:"shared rate limit burst"`
	Rate      float64 `name:"rate" default:"2" help:"shared refill, calls per second"`
}

type mcpInstallCmd struct {
	Client  string `arg:"" enum:"claude-code,claude-desktop,cursor,vscode" help:"which client's config to write"`
	Project bool   `help:"write .mcp.json in the current directory instead of the user's config"`
	Print   bool   `help:"print the JSON instead of writing a file"`
}

type mcpDoctorCmd struct{}

type helpCmd struct{}

type lsCmd struct {
	Project bool `help:"only VMs declared by the stoat.toml in this directory"`
}

type statusCmd struct{}

type initCmd struct {
	Name string `help:"project name; defaults to the directory name"`
}
type imagesCmd struct{}
type doctorCmd struct{}
type versionCmd struct{}

type getCmd struct {
	VM string `arg:"" help:"vm name"`
}

type upCmd struct {
	VM      string `arg:"" optional:"" help:"vm name; omit at project scope for every declared VM"`
	NoApply bool   `name:"no-apply" aliases:"no-provision" help:"start only; skip the automatic post-boot apply"`
}

type downCmd struct {
	VM string `arg:"" optional:"" help:"vm name; omit at project scope for every declared VM"`
}

type sshCmd struct {
	VM string `arg:"" help:"vm name"`
}

type sshCmdCmd struct {
	VM string `arg:"" help:"vm name"`
}

type rmCmd struct {
	VM  string `arg:"" optional:"" help:"vm name; omit at project scope for every declared VM"`
	Yes bool   `short:"y" help:"skip the delete confirmation"`
}

type createCmd struct {
	Name    string `arg:"" help:"new vm name"`
	Image   string `required:"" help:"catalog image id, or a path to your own image"`
	OS      string `help:"override the guest OS inferred from a byo image's filename"`
	Backend string `help:"override the backend inferred from a byo image's filename"`
	Mode    string `help:"live or disk (alpine iso only; every other image has one mode)"`
	RAM     int    `help:"memory in MB"`
	// name:"cpus" is required: kong's camelCase splitter reads "CPUs" as
	// "CP"+"Us" and would otherwise name the flag --cp-us.
	CPUs            int      `name:"cpus" help:"vcpu count"`
	Disk            string   `help:"disk size, absolute only (8G, 512M)"`
	Share           string   `help:"host directory to expose in the guest"`
	ConsolePassword string   `help:"console password; \"random\" generates one"`
	Recipes         []string `help:"recipe names to record on the VM"`
	Set             []string `help:"set a recipe param: <recipe>.<param>=<value>"`
	Secret          []string `help:"set a secret recipe param"`
	// AllowExec is a hidden alias of --agent-access, and both are pointers
	// so toArgs can tell "not given" from every real value, the same
	// pointer trick updateCmd's fields use (see grammar's type comment).
	// true maps to the exec level, false to manage; an explicit
	// --agent-access always wins over this alias.
	AllowExec   *bool   `name:"allow-exec" hidden:"" help:"alias of --agent-access exec (true) or manage (false)"`
	AgentAccess *string `name:"agent-access" enum:"none,observe,manage,exec" help:"what an MCP agent may do in this VM"`
}

// updateCmd's pointers are the point: see the type comment on grammar.
type updateCmd struct {
	VM      string    `arg:"" help:"vm name"`
	RAM     *int      `help:"memory in MB"`
	CPUs    *int      `name:"cpus" help:"vcpu count"`
	SSHPort *int      `name:"ssh-port" help:"host port forwarded to the guest's sshd"`
	Disk    *string   `help:"disk size, absolute only and grow-only (16G)"`
	Share   *string   `help:"host directory to expose in the guest; empty clears it"`
	Recipes *[]string `help:"replace the recipe list; empty clears it"`
	Set     []string  `help:"set a recipe param: <recipe>.<param>=<value>"`
	Unset   []string  `help:"clear a recipe param back to its manifest default"`
	Secret  []string  `help:"set a secret recipe param"`
	// AgentAccess is unrestricted here: only the MCP update tool may lower,
	// never raise, a VM's level. The CLI and TUI may do either.
	AgentAccess *string `name:"agent-access" enum:"none,observe,manage,exec" help:"change what an MCP agent may do in this VM"`
}

type cloneCmd struct {
	Source string `arg:"" help:"vm to copy"`
	Name   string `arg:"" help:"new vm name"`
}

type execCmd struct {
	VM string `arg:"" help:"vm name"`
	// passthrough on the POSITIONAL, never on the command. See grammar's
	// type comment.
	Command []string `arg:"" passthrough:"" help:"the guest command, verbatim"`
}

// cpCmd carries two mutually exclusive spellings: the positional scp/docker
// cp form for humans, and an explicit-flag form for a machine caller (§1.1 of
// docs/design/mcp-server.md). The flag form exists because a caller cannot
// always tell a host path with a colon in it from a "<vm>:<path>" compound.
// Both positionals are `optional:""` so kong accepts either form; toArgs
// enforces "both or neither, not a mix".
//
// Direction is a *string, not a plain enum string. kong's rule "enum value
// is only valid if it is either required or has a valid default" (tag.go)
// skips pointer, slice and map kinds. A default would silently pick the
// wrong direction when a caller forgets the flag. `required:""` would force
// the flag form's enum even when the positional form is used, since kong
// applies it to the struct field unconditionally. nil marks "the flag form
// was not used"; toArgs checks it.
type cpCmd struct {
	Source string `arg:"" optional:"" help:"source, either a host path or <vm>:<path>"`
	Dest   string `arg:"" optional:"" help:"destination, either a host path or <vm>:<path>"`

	VM        string  `help:"vm name (flag form; alternative to the positional <vm>:<path>)"`
	Direction *string `enum:"to,from" help:"to: --local to the guest; from: the guest to --local"`
	Local     string  `help:"host path (flag form)"`
	Remote    string  `help:"guest path (flag form)"`
}

type forwardCmd struct {
	VM    string   `arg:"" help:"vm name"`
	Pairs []string `arg:"" optional:"" help:"HOST:GUEST port pairs; none prints the current ones"`
	Clear bool     `help:"remove every forward from this VM"`
}

type pullCmd struct {
	ID string `arg:"" help:"catalog image id (see stoat images)"`
}

type snapshotCmd struct {
	VM      string `arg:"" help:"vm name"`
	Tag     string `arg:"" optional:"" help:"snapshot name; omit to list"`
	Restore bool   `xor:"action" help:"roll the VM back to <tag>, discarding everything since"`
	Delete  bool   `xor:"action" help:"remove <tag>"`
}

type pruneCmd struct {
	// --apply, not --dry-run: the destructive reading must be the one you
	// have to type on purpose.
	Apply  bool `help:"actually delete; without this, prune only reports"`
	Broken bool `help:"also remove VMs whose vm.toml will not parse"`
	Images bool `help:"also remove downloaded images no VM refers to"`
}

type waitCmd struct {
	VM      string        `arg:"" optional:"" help:"vm name; omit at project scope for every declared VM"`
	Until   string        `enum:"reachable,applied,stopped" default:"reachable" help:"state to wait for"`
	Healthy bool          `help:"wait for every applied recipe's health check to pass"`
	Timeout time.Duration `default:"2m" help:"give up after this long"`
}

type applyCmd struct {
	VM     string   `arg:"" optional:"" help:"vm name; omit at project scope for every declared VM"`
	Only   []string `help:"subset of the VM's own recipes"`
	DryRun bool     `name:"dry-run" help:"print what would run without running it"`
}

type recipesCmd struct {
	OS      string `help:"only recipes applicable to this guest OS"`
	Backend string `help:"only recipes applicable to this backend"`
}

type checkRecipesCmd struct {
	Names   []string `arg:"" help:"recipe names to check"`
	OS      string   `required:"" help:"guest OS to check against"`
	Backend string   `help:"backend to check against"`
}

type recipeCmd struct {
	List   recipeListCmd   `cmd:"" aliases:"ls" help:"list recipes with their scope and pinned commit"`
	New    recipeNewCmd    `cmd:"" help:"scaffold a recipe in the recipes directory"`
	Show   recipeShowCmd   `cmd:"" help:"print one recipe's params, outputs and health check"`
	Add    recipeAddCmd    `cmd:"" help:"install a recipe from the index or a git URL"`
	Lock   recipeLockCmd   `cmd:"" help:"resolve every declaration to a commit"`
	Sync   recipeSyncCmd   `cmd:"" help:"make the recipe cache match the lock"`
	Update recipeUpdateCmd `cmd:"" help:"fetch a recipe's ref again and repin it"`
	RM     recipeRmCmd     `cmd:"" name:"rm" help:"remove a remote recipe"`
	Search recipeSearchCmd `cmd:"" help:"search the recipe index"`
}

type recipeListCmd struct{}

type recipeNewCmd struct {
	Name    string `arg:"" help:"recipe name"`
	OS      string `help:"target OS for a new shell recipe"`
	Backend string `help:"\"cloudinit\" for a cloud-init fragment; shell otherwise"`
}

type recipeShowCmd struct {
	Name string `arg:"" help:"recipe name"`
}

type recipeAddCmd struct {
	Ref    string `arg:"" help:"index name, or a git URL, either with an optional @tag"`
	Yes    bool   `short:"y" help:"skip the confirmation for a URL source"`
	Global bool   `help:"use the home scope even inside a project"`
	Force  bool   `help:"install over a name that already exists"`
}

type recipeLockCmd struct {
	Global bool `help:"use the home scope even inside a project"`
}

type recipeSyncCmd struct {
	Global bool `help:"use the home scope even inside a project"`
}

type recipeUpdateCmd struct {
	Names  []string `arg:"" optional:"" help:"recipe names; omit for every remote recipe"`
	Global bool     `help:"use the home scope even inside a project"`
}

type recipeRmCmd struct {
	Name   string `arg:"" help:"recipe name"`
	Yes    bool   `short:"y" help:"skip the delete confirmation"`
	Global bool   `help:"use the home scope even inside a project"`
	Force  bool   `help:"remove it even while a VM lists it"`
}

type recipeSearchCmd struct {
	Term    []string `arg:"" optional:"" passthrough:"all" help:"match against name and description"`
	Refresh bool     `help:"fetch the index even when it is fresh"`
}

type recipeGuestCmd struct {
	LS   guestLsCmd   `cmd:"" name:"ls" help:"one line per guest: name, init, package manager, backend, source"`
	Show guestShowCmd `cmd:"" help:"the merged definition of one guest"`
}

type guestLsCmd struct{}

type guestShowCmd struct {
	Name string `arg:"" help:"guest name (see guest ls)"`
}

type logsCmd struct {
	VM    string `arg:"" optional:"" help:"vm name; omit to tail stoat's own log"`
	N     int    `short:"n" default:"50" help:"number of lines to tail"`
	Which string `enum:"console,apply" default:"console" help:"which log, with a vm name"`
}

type screenshotCmd struct {
	VM  string `arg:"" help:"vm name"`
	Out string `short:"o" help:"where to write the png; default <vm dir>/screenshots/<timestamp>.png"`
}

// toArgs flattens the selected command into the Args every runX consumes.
// Args stays the interface between parsing and running rather than
// threading the kong structs through 18 run functions directly.
func (g *grammar) toArgs(path string) (*Args, error) {
	a := &Args{Cmd: path, Quiet: g.Quiet}
	switch path {
	case "ls":
		a.Clear = g.LS.Project

	case "images", "doctor", "version", "status":

	case "help":
		// The `help` COMMAND has to render the text itself; only the -h/--help
		// FLAG path gets it from kong's own buffer.
		a.Help = helpText()

	case "get", "down", "ssh", "ssh-command":
		a.VM = g.vmFor(path)

	case "up":
		a.VM = g.Up.VM
		a.NoApply = g.Up.NoApply

	case "rm":
		a.VM, a.Yes = g.RM.VM, g.RM.Yes

	case "create":
		c := g.Create
		a.VM = c.Name
		// Absent --allow-exec keeps Spec.AllowExec's own default of true
		// (see Spec.AllowExec's doc comment); given, it also sets the
		// access level, per --allow-exec's alias contract.
		allowExec := true
		if c.AllowExec != nil {
			allowExec = *c.AllowExec
		}
		// An explicit --agent-access always wins over the hidden
		// --allow-exec alias; only when it is unset does --allow-exec pick
		// the level, and only when neither is given is the default manage.
		access := "manage"
		switch {
		case c.AgentAccess != nil:
			access = *c.AgentAccess
		case c.AllowExec != nil:
			access = "manage"
			if *c.AllowExec {
				access = "exec"
			}
		}
		a.Spec = core.Spec{
			Name: c.Name, Image: c.Image, OS: c.OS, Backend: c.Backend, Mode: c.Mode,
			RAM: c.RAM, CPUs: c.CPUs, Disk: c.Disk, Share: c.Share,
			ConsolePassword: c.ConsolePassword, Recipes: trimList(c.Recipes),
			AllowExec:   &allowExec,
			AgentAccess: access,
		}
		edits, err := parseParamFlags(c.Set, nil, c.Secret)
		if err != nil {
			return nil, err
		}
		a.Params = edits

	case "update":
		u := g.Update
		a.VM = u.VM
		a.Patch = core.Patch{
			RAM: u.RAM, CPUs: u.CPUs, SSHPort: u.SSHPort,
			Disk: u.Disk, Share: u.Share, AgentAccess: u.AgentAccess,
		}
		if u.Recipes != nil {
			// The POINTER carries "was it given"; the slice it points at
			// carries the new list. `--recipes ""` gives a non-nil pointer to
			// an empty list, which is the instruction to clear, so the pointer
			// must stay non-nil after trimming.
			trimmed := trimList(*u.Recipes)
			a.Patch.Recipes = &trimmed
		}
		// Wire names, and the order is fixed here rather than left to a map
		// walk so the list a caller sees is stable between runs.
		for _, f := range []struct {
			name string
			set  bool
		}{
			{"agent_access", u.AgentAccess != nil}, {"cpus", u.CPUs != nil}, {"disk", u.Disk != nil}, {"ram", u.RAM != nil},
			{"recipes", u.Recipes != nil}, {"share", u.Share != nil}, {"ssh_port", u.SSHPort != nil},
		} {
			if f.set {
				a.Changed = append(a.Changed, f.name)
			}
		}
		edits, err := parseParamFlags(u.Set, u.Unset, u.Secret)
		if err != nil {
			return nil, err
		}
		a.Params = edits
		if len(edits) > 0 {
			a.Changed = append(a.Changed, "params")
		}
		if len(a.Changed) == 0 {
			return nil, usageError("update: nothing to change; pass at least one of --ram --cpus --disk --share --ssh-port --recipes --set --unset --secret")
		}

	case "clone":
		a.VM, a.Tag = g.Clone.Source, g.Clone.Name

	case "exec":
		// No empty-command check: kong's own `expected "<command> ..."` fires
		// first, so one here would be unreachable.
		a.VM, a.Command = g.Exec.VM, g.Exec.Command

	case "cp":
		c := g.CP
		positional := c.Source != "" || c.Dest != ""
		flagged := c.VM != "" || c.Direction != nil || c.Local != "" || c.Remote != ""
		if positional && flagged {
			return nil, usageError("cp: use either the positional form or --vm/--direction/--local/--remote, not both")
		}

		var vm, remote, local string
		var toRemote bool
		switch {
		case flagged:
			if c.VM == "" || c.Direction == nil || c.Local == "" || c.Remote == "" {
				return nil, usageError("cp: the flag form needs --vm, --direction, --local and --remote together")
			}
			vm, remote, local, toRemote = c.VM, c.Remote, c.Local, *c.Direction == "to"
		case positional:
			if c.Source == "" || c.Dest == "" {
				return nil, usageError("cp: needs a source and a destination")
			}
			var err error
			vm, remote, local, toRemote, err = splitCopyArgs(c.Source, c.Dest)
			if err != nil {
				return nil, err
			}
		default:
			return nil, usageError("cp: needs either a source and destination, or --vm/--direction/--local/--remote")
		}

		// Resolved here, once, so both forms produce the same absolute path
		// and runCopy (run_access.go) never has to know the difference. This
		// is also what the JSON contract's "local" field echoes back: a
		// caller that authorised a relative or ~-prefixed path can verify
		// what actually ran, not what it typed (docs/design/mcp-server.md §1.1).
		abs, err := resolveLocal(local)
		if err != nil {
			return nil, usageError("cp: local path: " + err.Error())
		}
		a.VM, a.Remote, a.Local, a.ToRemote = vm, remote, abs, toRemote

	case "forward":
		f := g.Forward
		if f.Clear && len(f.Pairs) > 0 {
			return nil, usageError("forward: --clear takes no port pairs")
		}
		fwds, err := core.ParseForwards(f.Pairs)
		if err != nil {
			return nil, usageError("forward: " + err.Error())
		}
		a.VM, a.Forwards, a.Clear = f.VM, fwds, f.Clear

	case "pull":
		a.VM = g.Pull.ID

	case "init":
		a.Tag = g.Init.Name

	case "snapshot":
		// The mutual exclusion of --restore and --delete is kong's, via
		// xor:"action"; only the "needs a tag" rule is left here.
		s := g.Snapshot
		if (s.Restore || s.Delete) && s.Tag == "" {
			return nil, usageError("snapshot: --restore and --delete need a tag")
		}
		a.VM, a.Tag, a.Restore, a.Delete = s.VM, s.Tag, s.Restore, s.Delete

	case "prune":
		a.Prune = core.PruneOpts{DryRun: !g.Prune.Apply, Broken: g.Prune.Broken, Images: g.Prune.Images}

	case "wait":
		w := g.Wait
		if w.Timeout <= 0 {
			return nil, usageError("wait: --timeout must be positive")
		}
		if w.Healthy {
			if w.Until != "reachable" {
				return nil, usageError("wait: --healthy and --until are two different waits; pass one")
			}
			a.Until = core.UntilHealthy
		} else {
			a.Until = core.Until(w.Until)
		}
		a.VM, a.Timeout = w.VM, w.Timeout

	case "apply":
		a.VM, a.Only, a.DryRun = g.Apply.VM, trimList(g.Apply.Only), g.Apply.DryRun

	case "recipes":
		a.OS, a.Backend = g.Recipes.OS, g.Recipes.Backend

	case "check-recipes":
		a.Only, a.OS, a.Backend = trimList(g.CheckRecipes.Names), g.CheckRecipes.OS, g.CheckRecipes.Backend
		if len(a.Only) == 0 {
			return nil, usageError("check-recipes: name at least one recipe")
		}

	case "recipe list":
		a.Cmd, a.Sub = "recipe", "list"

	case "recipe new":
		n := g.Recipe.New
		a.Cmd, a.Sub = "recipe", "new"
		a.VM, a.OS, a.Backend = n.Name, n.OS, n.Backend

	case "recipe show":
		a.Cmd, a.Sub = "recipe", "show"
		a.VM = g.Recipe.Show.Name

	case "recipe add":
		c := g.Recipe.Add
		a.Cmd, a.Sub = "recipe", "add"
		a.Ref, a.Yes, a.Global, a.Force = c.Ref, c.Yes, c.Global, c.Force

	case "recipe lock":
		a.Cmd, a.Sub = "recipe", "lock"
		a.Global = g.Recipe.Lock.Global

	case "recipe sync":
		a.Cmd, a.Sub = "recipe", "sync"
		a.Global = g.Recipe.Sync.Global

	case "recipe update":
		a.Cmd, a.Sub = "recipe", "update"
		a.Names, a.Global = trimList(g.Recipe.Update.Names), g.Recipe.Update.Global

	case "recipe rm":
		c := g.Recipe.RM
		a.Cmd, a.Sub = "recipe", "rm"
		a.Names, a.Yes, a.Global, a.Force = []string{c.Name}, c.Yes, c.Global, c.Force

	case "recipe search":
		a.Cmd, a.Sub = "recipe", "search"
		term := g.Recipe.Search.Term
		if len(term) > 0 && term[0] == "--" {
			term = term[1:]
		}
		a.Ref, a.Refresh = strings.Join(term, " "), g.Recipe.Search.Refresh

	case "guest ls":
		a.Cmd, a.Sub = "guest", "ls"

	case "guest show":
		a.Cmd, a.Sub = "guest", "show"
		a.VM = g.Guest.Show.Name

	case "logs":
		l := g.Logs
		a.VM, a.N, a.Which = l.VM, l.N, core.Which(l.Which)

	case "screenshot":
		a.VM, a.Out = g.Screenshot.VM, g.Screenshot.Out

	case "mcp serve":
		m := g.MCP.Serve
		a.Cmd, a.Sub = "mcp", "serve"
		a.HTTP = m.HTTP
		a.Limits = mcpsrv.Limits{ToolBurst: m.ToolBurst, ToolRate: m.ToolRate, Burst: m.Burst, Rate: m.Rate}

	case "mcp install":
		i := g.MCP.Install
		a.Cmd, a.Sub = "mcp", "install"
		a.Client, a.InstallProject, a.Print = i.Client, i.Project, i.Print

	case "mcp doctor":
		a.Cmd, a.Sub = "mcp", "doctor"

	default:
		return nil, usageError("unknown subcommand " + path)
	}
	return a, nil
}

// trimList splits each element on commas and trims what is left.
//
//   - Kong splits a FLAG's value on commas but keeps surrounding whitespace,
//     so `--only "a.sh, b.sh"` arrives as {"a.sh", " b.sh"}.
//   - Kong never comma-splits a POSITIONAL, so `check-recipes a.sh,b.sh`
//     arrives as one element containing a comma.
//
// Splitting both here makes a positional list and a flag list behave the
// same. It returns nil for an all-blank list, so "cleared" stays
// distinguishable from "not given" by the pointer, never by the length.
func trimList(in []string) []string {
	var out []string
	for _, elem := range in {
		for _, s := range strings.Split(elem, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// splitCopyArgs resolves `cp <src> <dst>` into a VM, a remote path, a local
// path and a direction, from which side carried the "<vm>:" prefix. The
// scp/docker cp spelling, so there is no --to/--from flag to get backwards.
func splitCopyArgs(src, dst string) (vm, remote, local string, toRemote bool, err error) {
	srcVM, srcPath, srcRemote := strings.Cut(src, ":")
	dstVM, dstPath, dstRemote := strings.Cut(dst, ":")
	switch {
	case srcRemote == dstRemote:
		// Both or neither: guest-to-guest is not something one scp invocation
		// can do across two forwarded ports, and host-to-host is just `cp`.
		return "", "", "", false, usageError("cp: exactly one side must be <vm>:<path>")
	case srcRemote:
		return srcVM, srcPath, dst, false, nil
	default:
		return dstVM, dstPath, src, true, nil
	}
}

// resolveLocal turns a cp local path into an absolute one, expanding a
// leading ~ the same way config.VM.Share already does (config.Expand),
// rather than a second implementation of that rule. A relative path resolves
// against the process's cwd via filepath.Abs, the only meaning "relative"
// can have for a CLI invocation.
func resolveLocal(p string) (string, error) {
	return filepath.Abs(config.Expand(p))
}

// vmFor reads the VM name off whichever single-positional command was
// selected. They differ only by struct, so a switch here beats six near
// identical cases in toArgs.
func (g *grammar) vmFor(path string) string {
	switch path {
	case "get":
		return g.Get.VM
	case "up":
		return g.Up.VM
	case "down":
		return g.Down.VM
	case "ssh":
		return g.SSH.VM
	case "ssh-command":
		return g.SSHCmd.VM
	}
	return ""
}
