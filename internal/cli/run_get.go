package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/provider"
)

func runGet(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, wire.VMStatusResult{VM: wire.FromVMStatus(v, core.GraphicalSession())})
	}
	warnDeadline(stderr, v)
	fmt.Fprintf(stdout, "name: %s\n", v.Name)
	fmt.Fprintf(stdout, "os: %s\n", v.OS)
	fmt.Fprintf(stdout, "mode: %s\n", v.Mode)
	printProviderBlock(stdout, v)
	fmt.Fprintf(stdout, "backend: %s\n", v.Backend)
	fmt.Fprintf(stdout, "state: %s\n", v.State)
	fmt.Fprintf(stdout, "cpus: %d\n", v.CPUs)
	fmt.Fprintf(stdout, "ram: %d\n", v.RAM)
	fmt.Fprintf(stdout, "disk: %s\n", v.Disk)
	fmt.Fprintf(stdout, "share: %s\n", v.Share)
	fmt.Fprintf(stdout, "ssh port: %d\n", v.SSHPort)
	fmt.Fprintf(stdout, "ssh user: %s\n", v.SSHUser)
	fmt.Fprintf(stdout, "recipes: %s\n", strings.Join(v.Recipes, ", "))
	for _, state := range v.RecipeStates {
		status := "pending"
		if state.Applied {
			status = "applied " + state.Version
		}
		fmt.Fprintf(stdout, "  %-14s %-18s health %s\n", state.Name, status, state.Health)
		for _, name := range sortedKeys(state.Params) {
			fmt.Fprintf(stdout, "    param %-12s %s\n", name, state.Params[name])
		}
		for _, name := range sortedKeys(state.Outputs) {
			fmt.Fprintf(stdout, "    out   %-12s %s\n", name, state.Outputs[name])
		}
	}
	forwards := make([]string, len(v.Forwards))
	for i, f := range v.Forwards {
		forwards[i] = fmt.Sprintf("%d:%d", f.HostPort, f.GuestPort)
	}
	fmt.Fprintf(stdout, "forwards: %s\n", strings.Join(forwards, ", "))
	// The one line here that is not a vm.toml field: where the screen is. A
	// user who has lost their window asks `get` before they ask anything
	// else, and until now it was the one place that did not answer.
	printDisplay(stdout, core.DisplayFor(v, core.GraphicalSession()))
	if v.State == core.StateBroken {
		fmt.Fprintf(stdout, "error: %s\n", v.Error)
	}
	return ExitOK
}

// printProviderBlock prints the provider line, and for a non-qemu VM the
// project, zone, machine type, address and the nearer of its two deadlines.
func printProviderBlock(stdout io.Writer, v core.VM) {
	name := v.Provider
	if name == "" {
		name = "qemu"
	}
	fmt.Fprintf(stdout, "provider: %s\n", name)
	if v.Provider == "" {
		return
	}
	if v.GCEProject != "" {
		fmt.Fprintf(stdout, "gcp project: %s\n", v.GCEProject)
	}
	if v.GCEZone != "" {
		fmt.Fprintf(stdout, "zone: %s\n", v.GCEZone)
	}
	if v.MachineType != "" {
		fmt.Fprintf(stdout, "machine type: %s\n", v.MachineType)
	}
	if v.Address != "" {
		fmt.Fprintf(stdout, "address: %s\n", v.Address)
	}
	if when, which, ok := provider.Nearest(v.HardDeadline, v.SoftDeadline, time.Now()); ok {
		fmt.Fprintf(stdout, "expires: %s (in %s, %s)\n", when.UTC().Format(time.RFC3339), formatDuration(time.Until(when)), which)
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
