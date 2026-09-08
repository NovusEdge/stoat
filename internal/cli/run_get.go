package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

func runGet(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Get(a.VM)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, wire.VMStatusResult{VM: wire.FromVMStatus(v, core.GraphicalSession())})
	}
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
// project and zone it was created in. Machine type, address and the two
// deadlines are not printed: nothing in the Provider interface exposes them
// yet (Status carries only a running bit and the provider's raw status
// word), so showing them here would mean guessing rather than reporting.
func printProviderBlock(stdout io.Writer, v core.VM) {
	provider := v.Provider
	if provider == "" {
		provider = "qemu"
		fmt.Fprintf(stdout, "provider: %s\n", provider)
		return
	}
	fmt.Fprintf(stdout, "provider: %s\n", provider)
	cfg, err := config.Load(v.Name)
	if err != nil {
		return
	}
	if cfg.GCEProject != "" {
		fmt.Fprintf(stdout, "gcp project: %s\n", cfg.GCEProject)
	}
	if cfg.GCEZone != "" {
		fmt.Fprintf(stdout, "zone: %s\n", cfg.GCEZone)
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
