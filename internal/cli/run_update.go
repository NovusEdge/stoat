package cli

import (
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
	set, unset, secrets := paramMaps(a.Params)
	a.Patch.SetParams = set
	a.Patch.UnsetParams = unset
	a.Patch.Secrets = secrets
	v, err := core.Update(a.VM, a.Patch)
	if err != nil {
		return a.fail(stdout, stderr, err)
	}
	if a.JSON {
		return a.ok(stdout, map[string]any{
			"vm":         wire.FromVM(v, core.GraphicalSession()),
			"changed":    a.Changed,
			"applies_at": appliesAt(v, a.Changed),
		})
	}
	if !a.Quiet {
		a.prose(stdout).Done("updated %s: %v", v.Name, a.Changed)
		if appliesAt(v, a.Changed) == "next_start" {
			a.prose(stdout).Warn("%s is running. this takes effect at next start", v.Name)
		}
	}
	return ExitOK
}

// restartFields are the update fields qemu reads at start, so a running VM
// keeps its current values until it is restarted. Every other mutable field
// is read fresh by whatever next uses it: recipes and params by the next
// apply, agent_access by the next MCP call, installed by the next start
// decision. A disk grow is refused outright while a VM runs.
var restartFields = map[string]bool{
	"cpus": true, "disk": true, "display": true,
	"ram": true, "share": true, "ssh_port": true,
}

// appliesAt reports when an accepted edit becomes real. It answers for the
// call, not per field, so one restart-bound field in a mixed update makes the
// whole answer next_start.
func appliesAt(v core.VM, changed []string) string {
	if v.State != core.StateRunning {
		return "now"
	}
	for _, f := range changed {
		if restartFields[f] {
			return "next_start"
		}
	}
	return "now"
}
