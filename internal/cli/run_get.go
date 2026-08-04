package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

func runGet(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"vm": wire.FromVM(v)})
	}
	fmt.Fprintf(stdout, "name: %s\n", v.Name)
	fmt.Fprintf(stdout, "os: %s\n", v.OS)
	fmt.Fprintf(stdout, "mode: %s\n", v.Mode)
	fmt.Fprintf(stdout, "backend: %s\n", v.Backend)
	fmt.Fprintf(stdout, "state: %s\n", v.State)
	fmt.Fprintf(stdout, "cpus: %d\n", v.CPUs)
	fmt.Fprintf(stdout, "ram: %d\n", v.RAM)
	fmt.Fprintf(stdout, "disk: %s\n", v.Disk)
	fmt.Fprintf(stdout, "share: %s\n", v.Share)
	fmt.Fprintf(stdout, "ssh port: %d\n", v.SSHPort)
	fmt.Fprintf(stdout, "ssh user: %s\n", v.SSHUser)
	fmt.Fprintf(stdout, "recipes: %s\n", strings.Join(v.Recipes, ", "))
	forwards := make([]string, len(v.Forwards))
	for i, f := range v.Forwards {
		forwards[i] = fmt.Sprintf("%d:%d", f.HostPort, f.GuestPort)
	}
	fmt.Fprintf(stdout, "forwards: %s\n", strings.Join(forwards, ", "))
	// The one line here that is not a vm.toml field: where the screen is. A
	// user who has lost their window asks `get` before they ask anything
	// else, and until now it was the one place that did not answer.
	printDisplay(stdout, core.DisplayFor(v))
	if v.State == core.StateBroken {
		fmt.Fprintf(stdout, "error: %s\n", v.Error)
	}
	return ExitOK
}

func runSSHCommand(a *Args, stdout, stderr io.Writer) int {
	argv, err := core.SSHCommand(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{"argv": argv})
	}
	fmt.Fprintln(stdout, strings.Join(argv, " "))
	return ExitOK
}
