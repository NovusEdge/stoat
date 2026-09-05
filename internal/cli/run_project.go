package cli

import (
	"fmt"
	"io"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// fanOut runs one over every declared VM in declaration order.
//
// It stops at the first failure. A project is a set of VMs a contributor
// expects to come up together, and carrying on after one fails produces a
// half-built project the user has to reconstruct from the log. Every VM after
// the failure is reported as skipped, so what did not run is as visible as
// what did.
func fanOut(a *Args, stdout, stderr io.Writer, one func(name string) error) int {
	var runs []wire.ProjectRunVM
	failed := false
	for _, d := range a.Project.VMs {
		name := a.Project.GlobalName(d.Key)
		entry := wire.ProjectRunVM{Key: d.Key, Name: name, Status: "ok"}
		switch {
		case failed:
			entry.Status = "skipped"
		default:
			if err := one(name); err != nil {
				entry.Status, entry.Error, failed = "error", err.Error(), true
			}
		}
		runs = append(runs, entry)
		if !a.Quiet {
			fmt.Fprintf(stdout, "%-12s %s\n", d.Key, entry.Status)
			if entry.Error != "" {
				fmt.Fprintf(stderr, "stoat: %s: %s: %s\n", a.Cmd, d.Key, entry.Error)
			}
		}
	}
	if a.JSON {
		code := ExitOK
		if failed {
			code = ExitFail
		}
		_ = wire.NewEmitter(stdout).ResultOKCode(a.Cmd, wire.FromProjectRun(a.Project.Name, runs), !failed)
		return code
	}
	if failed {
		return ExitFail
	}
	return ExitOK
}
