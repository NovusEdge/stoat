package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/project"
)

// runStatus prints one line per declared VM: global name, state, health and
// drift. It changes nothing, so it is safe to run against a project someone
// else is building.
func runStatus(a *Args, stdout, stderr io.Writer) int {
	if a.Project == nil {
		return a.failMsg(stdout, stderr, core.ErrNotFound, "no "+project.FileName+" in this directory")
	}

	var rows []wire.ProjectStatusVM
	for _, d := range a.Project.VMs {
		name := a.Project.GlobalName(d.Key)
		row := wire.ProjectStatusVM{Key: d.Key, Name: name, State: "missing", Health: "", Drift: []wire.Drift{}}

		v, err := core.Get(name)
		switch {
		case errors.Is(err, core.ErrNotFound):
		case err != nil:
			return a.fail(stdout, stderr, err)
		default:
			row.State, row.Health = string(v.State), string(v.Health)
			drift, err := core.Diff(a.Project, d.Key)
			if err != nil {
				// An immutable change is a real answer about this VM, not a
				// reason to refuse the whole table.
				row.Drift = []wire.Drift{{Field: "image", From: "", To: "", NeedsRestart: true}}
				fmt.Fprintf(stderr, "stoat: status: %v\n", err)
			} else {
				row.Drift = wire.FromDrifts(drift)
			}
		}
		rows = append(rows, row)
	}

	if a.JSON {
		return a.ok(stdout, wire.FromProjectStatus(a.Project.Name, a.Project.Dir, rows))
	}
	fmt.Fprintf(stdout, "%-12s %-20s %-9s %-9s %s\n", "KEY", "NAME", "STATE", "HEALTH", "DRIFT")
	for _, r := range rows {
		fmt.Fprintf(stdout, "%-12s %-20s %-9s %-9s %s\n",
			r.Key, r.Name, r.State, dash(r.Health), dash(renderDrift(r.Drift)))
	}
	return ExitOK
}

// renderDrift is the DRIFT column: "cpus 2 → 4 (restart), recipes a → a,b".
func renderDrift(ds []wire.Drift) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = fmt.Sprintf("%s %s → %s", d.Field, dash(d.From), dash(d.To))
		if d.NeedsRestart {
			parts[i] += " (restart)"
		}
	}
	return strings.Join(parts, ", ")
}

// dash renders an empty value as "-", so a column never reads as a missing
// field.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
