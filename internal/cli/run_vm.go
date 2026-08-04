package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/core"
)

func runLS(a *Args, stdout, stderr io.Writer) int {
	vms, err := core.List()
	if err != nil {
		fmt.Fprintln(stderr, "stoat: ls:", err)
		return ExitFail
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
		fmt.Fprintln(stderr, "stoat: up:", err)
		return ExitFail
	}
	// core.Get reports a broken VM as StateBroken rather than an error, so it
	// can be listed and deleted. Neither is true of starting one: refuse here,
	// before any output, rather than printing "starting x..." and only then
	// failing: a progress line that announces something the next line admits
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
		// so this only fires on the started-after-the-check race, but it's
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

// runClone copies a VM. core.Clone refuses a running source, allocates a fresh
// ssh port and drops the source's port forwards. Say so, because a user who
// expected the forwards to come along would otherwise find out by debugging.
func runClone(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Clone(a.VM, a.Tag)
	if err != nil {
		fmt.Fprintln(stderr, "stoat: clone:", err)
		return ExitFail
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "cloned %s to %s (ssh :%d)\n", a.VM, v.Name, v.SSHPort)
		fmt.Fprintf(stdout, "port forwards were not copied; set them with: stoat forward %s ...\n", v.Name)
	}
	return ExitOK
}
