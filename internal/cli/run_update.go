package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

// runUpdate changes a VM in place. Parse already did the load-bearing work:
// only the flags actually given become non-nil pointers on the Patch, so a
// field nobody mentioned is left alone rather than silently zeroed.
//
// changed is reported back because a caller cannot otherwise tell which of its
// requested edits core accepted, and applies_at because most of them are
// written to vm.toml now and only take effect at the VM's next start.
func runUpdate(a *Args, stdout, stderr io.Writer) int {
	v, err := core.Update(a.VM, a.Patch)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm":         wire.FromVM(v, core.GraphicalSession()),
			"changed":    a.Changed,
			"applies_at": appliesAt(v),
		})
	}
	if !a.Quiet {
		fmt.Fprintf(stdout, "updated %s: %v\n", v.Name, a.Changed)
		if appliesAt(v) == "next_start" {
			fmt.Fprintf(stdout, "%s is running; this takes effect at next start\n", v.Name)
		}
	}
	return ExitOK
}

// appliesAt reports when an accepted edit becomes real. Every mutable field
// except Recipes is read by qemu at start, so a running VM keeps its current
// values until it is restarted. Recipes only affects the next Apply, but a
// caller cannot act on a per-field answer through one CLI invocation, so the
// conservative one is reported for the whole call.
func appliesAt(v core.VM) string {
	if v.State == core.StateRunning {
		return "next_start"
	}
	return "now"
}
