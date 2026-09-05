package mcpsrv

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/project"
)

// projectIn is the input of every project tool. It is empty on purpose: the
// scope is the server's own working directory, which stoat mcp install writes
// into the client's entry, and a caller cannot point the server elsewhere.
type projectIn struct{}

// requireProject answers with the loaded stoat.toml, or with the error a
// caller can act on. A file that will not parse reports the parse error, not
// "no project": those are different problems with different fixes.
func (s *srv) requireProject() (*project.Project, error) {
	if s.projErr != nil {
		return nil, s.projErr
	}
	if s.proj == nil {
		return nil, fmt.Errorf("this server's working directory has no %s; run stoat init there, or use start, stop, apply_recipes and wait with a vm", project.FileName)
	}
	return s.proj, nil
}

// fanOut runs one over every declared VM in declaration order, stops at the
// first failure and reports the rest as skipped. It mirrors the CLI's own
// fan-out (internal/cli/run_project.go) so an agent and a shell see one
// behaviour.
func (s *srv) fanOut(one func(name, key string) error) (wire.ProjectRun, error) {
	p, err := s.requireProject()
	if err != nil {
		return wire.ProjectRun{}, err
	}
	var runs []wire.ProjectRunVM
	failed := false
	for _, d := range p.VMs {
		name := p.GlobalName(d.Key)
		entry := wire.ProjectRunVM{Key: d.Key, Name: name, Status: "ok"}
		switch {
		case failed:
			entry.Status = "skipped"
		default:
			if err := one(name, d.Key); err != nil {
				entry.Status, entry.Error, failed = "error", err.Error(), true
			}
		}
		runs = append(runs, entry)
	}
	return wire.FromProjectRun(p.Name, runs), nil
}

func (s *srv) registerProject(server *mcp.Server) {
	register(server, "project_status", classRead,
		"Report every VM the stoat.toml in this server's working directory declares: its declaration key, its global name, whether it exists yet, its health, and every field where the declaration and the VM disagree. Call it before project_up to see what that would change. It runs nothing and touches no VM. Read-only.",
		func(ctx context.Context, _ projectIn) (wire.ProjectStatus, error) {
			p, err := s.requireProject()
			if err != nil {
				return wire.ProjectStatus{}, err
			}
			var rows []wire.ProjectStatusVM
			for _, d := range p.VMs {
				name := p.GlobalName(d.Key)
				row := wire.ProjectStatusVM{Key: d.Key, Name: name, State: "missing", Drift: []wire.Drift{}}
				if v, err := core.Get(name); err == nil {
					row.State, row.Health = string(v.State), string(v.Health)
					if drift, err := core.Diff(p, d.Key); err == nil {
						row.Drift = wire.FromDrifts(drift)
					} else {
						row.Error = err.Error()
					}
				}
				rows = append(rows, row)
			}
			return wire.FromProjectStatus(p.Name, p.Dir, rows), nil
		})

	register(server, "project_up", classMutate,
		"Create and start every VM the stoat.toml in this server's working directory declares, in declaration order. A VM that does not exist yet is created from its declaration; an existing one takes every mutable change the declaration makes before it starts. It stops at the first failure and reports every later VM as skipped. Use start when you mean one named VM. Mutating: it creates VM directories and disks and it runs qemu.",
		func(ctx context.Context, _ projectIn) (wire.ProjectRun, error) {
			return s.fanOut(func(name, key string) error {
				if _, err := core.Reconcile(s.proj, key); err != nil {
					return err
				}
				return core.Start(name)
			})
		})

	register(server, "project_down", classMutate,
		"Stop every VM the stoat.toml in this server's working directory declares, in declaration order. It stops at the first failure and reports every later VM as skipped. Use stop when you mean one named VM. Reversible with project_up. Mutating: it shuts qemu down.",
		func(ctx context.Context, _ projectIn) (wire.ProjectRun, error) {
			return s.fanOut(func(name, _ string) error { return core.Stop(name) })
		})

	register(server, "project_apply", classMutate,
		"Run the configured recipes of every VM the stoat.toml in this server's working directory declares, in declaration order. A recipe body is arbitrary guest code, so each VM needs agent_access manage or higher and a VM below that fails its own entry. It stops at the first failure and reports every later VM as skipped. Mutating: it runs recipe scripts inside each guest, and it reaches outside this process.",
		func(ctx context.Context, _ projectIn) (wire.ProjectRun, error) {
			return s.fanOut(func(name, _ string) error {
				if err := requireAccess(name, LevelManage); err != nil {
					return err
				}
				return core.Apply(ctx, name, core.ApplyOpts{})
			})
		})

	register(server, "project_wait", classMutate,
		"Block until every VM the stoat.toml in this server's working directory declares answers on ssh, in declaration order. It stops at the first VM that does not, and reports every later VM as skipped. Use wait when you mean one named VM or a state other than reachable. Mutating only in that it blocks the caller.",
		func(ctx context.Context, _ projectIn) (wire.ProjectRun, error) {
			return s.fanOut(func(name, _ string) error {
				return core.Wait(ctx, name, core.UntilReachable)
			})
		})
}
