package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

func runLS(a *Args, stdout, stderr io.Writer) int {
	vms, err := core.List()
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"vms": wire.FromVMs(vms, core.GraphicalSession())})
	}

	fmt.Fprintf(stdout, "%-15s %-5s %-8s %-5s %-6s %s\n", "NAME", "MODE", "STATE", "CPUS", "RAM", "SSH")
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
	// after, the same reason it existed here originally.
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
		printDisplay(stdout, core.DisplayFor(started, core.GraphicalSession()))
	}
	return ExitOK
}

// printDisplay says where a VM's screen is and how to reach it.
//
// This prints on every start, not only the surprising one, because the
// surprising one is not detectable from here: a disk VM shows a window until
// setup-alpine marks it installed, and the start after that silently moves
// the screen to a VNC socket. A user who was never told the socket exists has
// no error to search for and no path to guess.
func printDisplay(w io.Writer, d core.Display) {
	switch d.Kind {
	case core.DisplayWindow:
		fmt.Fprintln(w, "display: a qemu window, for the OS installer's console")
	case core.DisplayVNC:
		if d.NoSession {
			// The install console, on a host that cannot open a window. Said
			// before the socket line, because without it "no qemu window" for a
			// VM that is mid-install reads as the thing that went wrong.
			// "no usable session" rather than "no session": the same line
			// prints when the user set STOAT_GRAPHICAL=0 on a host that plainly
			// has one, because its GTK cannot draw on it.
			fmt.Fprintln(w, "display: no usable graphical session on this host, so the OS installer's")
			fmt.Fprintln(w, "  console is on VNC instead; drive it from a machine with a screen")
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
		// The State check above already catches the common case before any
		// output prints; errors.Is here is the defensive/authoritative path
		// (a VM stopped between the check and this call) rather than the
		// primary one, and it's what actually produces today's exact message
		// for that race.
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
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if v.State == core.StateRunning {
		return a.failMsg(stdout, stderr, core.ErrAlreadyRunning, a.VM+" is running; stop it first")
	}
	if !a.Yes {
		if a.JSON {
			// --json never prompts (§1): an enforcement boundary a process can
			// cross by answering a prompt is not a boundary.
			return a.fail(stdout, stderr, fmt.Errorf("%w: %s", wire.ErrConfirmationRequired, a.VM))
		}
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
		// so this only fires on the started-after-the-check race, but it's
		// what reproduces today's exact "stop it first" message for it.
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
