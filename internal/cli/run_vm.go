package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/project"
	"github.com/novusedge/stoat/internal/sshx"
)

func runLS(a *Args, stdout, stderr io.Writer) int {
	vms, err := core.List()
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	core.AttachKeys(vms, a.Project)
	if a.Clear {
		if a.Project == nil {
			return a.failMsg(stdout, stderr, core.ErrNotFound, "--project needs a "+project.FileName+" in this directory")
		}
		var kept []core.VM
		for _, v := range vms {
			if v.Project == a.Project.Dir {
				kept = append(kept, v)
			}
		}
		vms = kept
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"vms": wire.FromVMs(vms, core.GraphicalSession())})
	}

	fmt.Fprintf(stdout, "%-15s %-5s %-8s %-5s %-6s %-6s %s\n", "NAME", "MODE", "STATE", "CPUS", "RAM", "SSH", "PROJECT")
	// core.List() sorts every VM, broken ones included, together by name,
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
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5d %-6d %-6d %s\n",
			v.Name, v.Mode, colorState(state, 8), v.CPUs, v.RAM, v.SSHPort, projectCell(v))
	}
	// Broken VMs are real entries: hiding them is the bug that was already
	// reported once. They get dashes for the fields a broken vm.toml can't
	// supply, plus a one-line reason.
	for _, v := range vms {
		if v.State != core.StateBroken {
			continue
		}
		fmt.Fprintf(stdout, "%-15s %-5s %s %-5s %-6s %-4s %-6s %s\n",
			v.Name, "-", colorState("broken", 8), "-", "-", "-", "-", oneLine(v.Error))
	}
	return ExitOK
}

// projectCell renders the PROJECT column: the declaring directory, marked
// when it is gone, and "-" for a VM stoat new created.
func projectCell(v core.VM) string {
	switch {
	case v.Project == "":
		return "-"
	case v.ProjectMissing:
		return v.Project + " (missing)"
	default:
		return v.Project
	}
}

func runUp(a *Args, stdout, stderr io.Writer) int {
	if a.VM == "" {
		// Reconcile every declaration before any of them starts. A VM whose
		// boot fails must not leave a sibling declared later without the
		// vm.toml that stoat status and stoat ls read: reconcile is cheap and
		// idempotent, so running it for the whole project up front is safe,
		// and only the start step itself needs to stop at the first failure.
		for _, d := range a.Project.VMs {
			if err := reconcileOne(a, d.Key, stdout); err != nil {
				return a.fail(stdout, stderr, err)
			}
		}
		return fanOut(a, stdout, stderr, func(name string) error {
			return upOne(a, name, stdout, stderr)
		})
	}
	if a.Project != nil {
		if key, ok := a.Project.KeyFor(a.VM); ok {
			if err := reconcileOne(a, key, stdout); err != nil {
				return a.fail(stdout, stderr, err)
			}
		}
	}
	// core.Get first: a caller must learn "no such VM" or "broken" before
	// any progress line prints, not after.
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	// core.Get reports a broken VM as StateBroken rather than an error, so it
	// can be listed and deleted. Neither is true of starting one: refuse here,
	// before any output, rather than printing "starting x..." and only then
	// failing: a progress line that announces something the next line admits
	// is impossible is worse than no line at all.
	if v.State == core.StateBroken {
		return a.failMsg(stdout, stderr, core.ErrBroken, v.Error)
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "starting %s...\n", a.VM)
	}
	if err := core.Start(a.VM); err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		// Re-read rather than emitting the pre-Start copy: state is the field a
		// caller acts on next, and the one this command just changed.
		if started, err := core.Get(a.VM); err == nil {
			v = started
		}
		return a.ok(stdout, map[string]any{"vm": wire.FromVM(v, core.GraphicalSession())})
	}
	fmt.Fprintf(stdout, "%s started (ssh :%d)\n", a.VM, v.SSHPort)
	// Re-read for the display line too: Start is what flips a disk VM to
	// installed, and that flip is exactly what moves the screen off the qemu
	// window. The pre-Start copy would announce a window that is not there.
	if started, err := core.Get(a.VM); err == nil {
		v = started
	}
	printDisplay(stdout, core.DisplayFor(v, core.GraphicalSession()))

	// An uninstalled disk VM's own installer is running now, not the system
	// `apply` needs to reach. The CLI is a foreground process, so it blocks
	// here rather than exiting and leaving the desktop half set up.
	// --no-apply skips the recipe run below, not this: the disk must still
	// boot the installed system either way.
	if v.Mode == "disk" && !v.Installed {
		if !a.Quiet {
			fmt.Fprintf(stdout, "installing %s (a few minutes)...\n", a.VM)
		}
		restarted, err := core.AutoRestartAfterInstall(context.Background(), a.VM)
		if err != nil || !restarted {
			fmt.Fprintf(stdout, "install did not finish; inspect: stoat logs %s\n", a.VM)
			return ExitOK
		}
		if started, err := core.Get(a.VM); err == nil {
			v = started
		}
	}
	return afterStart(a, v, stdout, stderr)
}

// afterStart runs v's pending recipes once it answers ssh, the same
// core.Apply path `stoat apply` uses (run_apply.go's runApply).
// --no-apply, or a VM with nothing pending, returns immediately: `up`
// does not block a VM that has no work waiting.
func afterStart(a *Args, v core.VM, stdout, stderr io.Writer) int {
	if a.NoApply {
		return ExitOK
	}
	cfg, err := config.Load(v.Name)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	needs, err := core.NeedsProvision(cfg)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if !needs {
		return ExitOK
	}

	if !a.Quiet {
		fmt.Fprintf(stdout, "waiting for ssh on %s...\n", a.VM)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sshx.WaitTimeout)
	defer cancel()
	if err := core.Wait(ctx, a.VM, core.UntilReachable); err != nil {
		return a.fail(stdout, stderr, err)
	}

	if !a.Quiet {
		fmt.Fprintf(stdout, "applying recipes to %s...\n", a.VM)
	}
	redactor, err := newSecretRedactor(v.Paths.Dir, stdout)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	done := make(chan error, 1)
	go func() { done <- core.Apply(context.Background(), a.VM, core.ApplyOpts{}) }()
	if err := streamFile(v.Paths.ApplyLog, redactor, done); err != nil {
		_ = redactor.Flush()
		if errors.Is(err, core.ErrProvisionInProgress) {
			// A concurrent `apply` already holds the lock; that run owns the
			// error. `up` still started the VM, so this is not a failure of
			// the up command.
			fmt.Fprintf(stdout, "%s: an apply is already running\n", a.VM)
			return ExitOK
		}
		return a.fail(stdout, stderr, err)
	}
	if err := redactor.Flush(); err != nil {
		return a.fail(stdout, stderr, err)
	}
	fmt.Fprintf(stdout, "%s: recipes applied\n", a.VM)
	return ExitOK
}

// reconcileOne makes one declared VM match stoat.toml before it starts. A
// change that needs a restart is already saved; the guest sees it at the next
// down and up, and that is what the line says.
func reconcileOne(a *Args, key string, stdout io.Writer) error {
	r, err := core.Reconcile(a.Project, key)
	if err != nil {
		return err
	}
	if a.Quiet {
		return nil
	}
	if r.Created {
		fmt.Fprintf(stdout, "created %s from %s\n", r.Name, project.FileName)
		return nil
	}
	for _, d := range r.Drift {
		suffix := ""
		if d.NeedsRestart {
			suffix = " (applies at the next down and up)"
		}
		fmt.Fprintf(stdout, "%s: %s %s -> %s%s\n", d.Key, d.Field, d.From, d.To, suffix)
	}
	return nil
}

// upOne is runUp's body for one VM, used by the no-argument fan-out. It
// returns an error rather than an exit code, which is what fanOut collects.
func upOne(a *Args, name string, stdout, stderr io.Writer) error {
	sub := *a
	sub.VM = name
	sub.JSON = false // the fan-out emits the single terminal result line
	if code := runUp(&sub, stdout, stderr); code != ExitOK {
		return fmt.Errorf("%s: up failed", name)
	}
	return nil
}

// printDisplay says where a VM's screen is and how to reach it.
//
// This prints on every start, not only the surprising one, because the
// surprising one is not detectable from here: a VM with display="vnc", or one
// on a host with no graphical session, has its screen on a socket instead of
// the window a user expects by default. A user who was never told the socket
// exists has no error to search for and no path to guess.
func printDisplay(w io.Writer, d core.Display) {
	switch d.Kind {
	case core.DisplayWindow:
		fmt.Fprintln(w, "display: a qemu window")
	case core.DisplayVNC:
		if d.NoSession {
			// Said before the socket line, because without it "no qemu window"
			// on its own reads as the thing that went wrong.
			// "no usable session" rather than "no session": the same line
			// prints when the user set STOAT_GRAPHICAL=0 on a host that plainly
			// has one, because its GTK cannot draw on it.
			fmt.Fprintln(w, "display: no usable graphical session on this host, so the screen")
			fmt.Fprintln(w, "  is on VNC instead; attach to watch it")
		}
		fmt.Fprintf(w, "display: no qemu window; the screen is on %s\n", d.Socket)
		if d.Attach.Command == "" {
			fmt.Fprintf(w, "  no VNC viewer found; install one of: %s\n", strings.Join(d.Attach.Missing, ", "))
			return
		}
		fmt.Fprintf(w, "  attach with: %s\n", d.Attach.Command)
		if d.Attach.Then != "" {
			fmt.Fprintf(w, "  %s\n", d.Attach.Then)
		}
	}
}

func runDown(a *Args, stdout, stderr io.Writer) int {
	if a.VM == "" {
		return fanOut(a, stdout, stderr, func(name string) error {
			sub := *a
			sub.VM, sub.JSON = name, false
			if code := runDown(&sub, stdout, stderr); code != ExitOK {
				return fmt.Errorf("%s is not running", name)
			}
			return nil
		})
	}
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	// Same reasoning as up: "not running" is true of a broken VM but says
	// nothing useful, and the parse error is the only thing that helps.
	if v.State == core.StateBroken {
		return a.failMsg(stdout, stderr, core.ErrBroken, v.Error)
	}
	if v.State != core.StateRunning {
		return a.failMsg(stdout, stderr, core.ErrNotRunning, a.VM+" is not running")
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "stopping %s...\n", a.VM)
	}
	if err := core.Stop(a.VM); err != nil {
		// The State check above catches the common case before any output
		// prints. This handles the race: a VM stopped between the check and
		// this call.
		if errors.Is(err, core.ErrNotRunning) {
			return a.failMsg(stdout, stderr, core.ErrNotRunning, a.VM+" is not running")
		}
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		if stopped, err := core.Get(a.VM); err == nil {
			v = stopped
		}
		return a.ok(stdout, map[string]any{"vm": wire.FromVM(v, core.GraphicalSession())})
	}
	fmt.Fprintf(stdout, "%s stopped\n", a.VM)
	return ExitOK
}

func runRM(a *Args, stdin io.Reader, stdout, stderr io.Writer) int {
	if a.VM == "" {
		return fanOut(a, stdout, stderr, func(name string) error {
			sub := *a
			sub.VM, sub.JSON = name, false
			if code := runRM(&sub, stdin, stdout, stderr); code != ExitOK {
				return fmt.Errorf("%s was not deleted", name)
			}
			return nil
		})
	}
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if v.State == core.StateRunning {
		return a.failMsg(stdout, stderr, core.ErrAlreadyRunning, a.VM+" is running; stop it first")
	}
	if ok, code := confirm(a, stdin, stdout, stderr, "delete VM "+a.VM+"?"); !ok {
		return code
	}
	if err := core.Destroy(a.VM); err != nil {
		// Same race as runDown: the State check above refuses a running VM
		// before the confirmation prompt, so this only fires when the VM
		// started between the check and this call.
		if errors.Is(err, core.ErrAlreadyRunning) {
			return a.failMsg(stdout, stderr, core.ErrAlreadyRunning, a.VM+" is running; stop it first")
		}
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"name": a.VM, "deleted": true})
	}
	fmt.Fprintf(stdout, "%s deleted\n", a.VM)
	return ExitOK
}

// runCreate is the whole of `stoat create`: hand the flags to core and print
// what came back. There was no create subcommand before core existed, because
// everything it needed lived inside the TUI's form.
func runCreate(a *Args, stdout, stderr io.Writer) int {
	set, _, secrets := paramMaps(a.Params)
	a.Spec.Params = set
	a.Spec.Secrets = secrets
	v, err := core.Create(a.Spec)
	if err != nil {
		if a.JSON {
			return a.fail(stdout, stderr, err)
		}
		fmt.Fprintln(stderr, "stoat: create:", err)
		if errors.Is(err, core.ErrImageNotDownloaded) {
			fmt.Fprintln(stderr, "stoat: download it from the TUI's image picker first")
		}
		return ExitFail
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"vm": wire.FromVM(v, core.GraphicalSession())})
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "created %s (%s, %s, ssh port %d)\n", v.Name, v.OS, v.Mode, v.SSHPort)
		fmt.Fprintf(stdout, "start it with: stoat up %s\n", v.Name)
	}
	return ExitOK
}

// runClone copies a VM. core.Clone refuses a running source, allocates a fresh
// ssh port and drops the source's port forwards. Say so, because a user who
// expected the forwards to come along would otherwise find out by debugging.
func runClone(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Clone(a.VM, a.Tag)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		// forwards_copied is emitted rather than left for the consumer to
		// notice: the dropped forwards are the surprise in this command.
		return a.ok(stdout, map[string]any{
			"vm":              wire.FromVM(v, core.GraphicalSession()),
			"source":          a.VM,
			"forwards_copied": false,
		})
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "cloned %s to %s (ssh :%d)\n", a.VM, v.Name, v.SSHPort)
		fmt.Fprintf(stdout, "port forwards were not copied; set them with: stoat forward %s ...\n", v.Name)
	}
	return ExitOK
}
