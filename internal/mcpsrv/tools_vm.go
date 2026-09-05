package mcpsrv

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

type createIn struct {
	Name        string   `json:"name" jsonschema:"name for the new VM"`
	Image       string   `json:"image" jsonschema:"catalog image id, see list_images"`
	OS          string   `json:"os,omitempty" jsonschema:"guest OS override"`
	Backend     string   `json:"backend,omitempty" jsonschema:"backend override"`
	Mode        string   `json:"mode,omitempty" jsonschema:"live or disk"`
	RAMMB       int      `json:"ram_mb,omitempty" jsonschema:"memory in MEGABYTES"`
	CPUs        int      `json:"cpus,omitempty" jsonschema:"vcpu count"`
	Disk        string   `json:"disk,omitempty" jsonschema:"disk size such as 8G"`
	Recipes     []string `json:"recipes,omitempty" jsonschema:"recipe names to record on the VM"`
	AgentAccess string   `json:"agent_access,omitempty" jsonschema:"what an agent may do in this VM: none, observe, manage or exec; manage is the default"`
}

type updateIn struct {
	VM          string                       `json:"vm" jsonschema:"name of the VM"`
	RAMMB       int                          `json:"ram_mb,omitempty" jsonschema:"memory in MEGABYTES"`
	CPUs        int                          `json:"cpus,omitempty" jsonschema:"vcpu count"`
	SSHPort     int                          `json:"ssh_port,omitempty" jsonschema:"host port forwarded to the guest sshd"`
	Disk        string                       `json:"disk,omitempty" jsonschema:"disk size, grow only"`
	Recipes     []string                     `json:"recipes,omitempty" jsonschema:"replace the recipe list"`
	Params      map[string]map[string]string `json:"params,omitempty" jsonschema:"recipe params, keyed by recipe name then param name"`
	Secrets     map[string]map[string]string `json:"secrets,omitempty" jsonschema:"recipe secrets, keyed by recipe name then param name; never echoed back"`
	AgentAccess string                       `json:"agent_access,omitempty" jsonschema:"lower this VM's agent access level; raising it is refused here and is a CLI or TUI action"`
}

type cloneIn struct {
	Source string `json:"source" jsonschema:"VM to copy"`
	Name   string `json:"name" jsonschema:"name for the copy"`
}

type snapshotIn struct {
	VM  string `json:"vm" jsonschema:"name of the VM"`
	Tag string `json:"tag" jsonschema:"snapshot tag"`
}

type forwardIn struct {
	VM    string   `json:"vm" jsonschema:"name of the VM"`
	Pairs []string `json:"pairs,omitempty" jsonschema:"HOST:GUEST port pairs; none and clear=false only shows the current forwards"`
	Clear bool     `json:"clear,omitempty" jsonschema:"remove every forward from this VM"`
}

type waitIn struct {
	VM             string `json:"vm" jsonschema:"name of the VM"`
	Until          string `json:"until,omitempty" jsonschema:"reachable, applied or stopped; reachable is the default"`
	Healthy        bool   `json:"healthy,omitempty" jsonschema:"also wait for every applied recipe's health check to pass"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"a plain count of seconds, not a duration string, capped at 600"`
}

type pruneIn struct {
	Apply  bool `json:"apply,omitempty" jsonschema:"actually delete; without this prune only reports"`
	Broken bool `json:"broken,omitempty" jsonschema:"also remove VM directories whose vm.toml will not parse"`
	Images bool `json:"images,omitempty" jsonschema:"also remove downloaded images no VM refers to"`
}

type applyIn struct {
	VM   string   `json:"vm" jsonschema:"name of the VM"`
	Only []string `json:"only,omitempty" jsonschema:"subset of the VM's own recipes"`
}

// waitTimeout clamps the caller's seconds. The stdio transport serves one
// request at a time, and wait is the only tool that blocks on purpose, so it
// is the only one that needs a ceiling a caller cannot raise.
func waitTimeout(seconds int) time.Duration {
	return time.Duration(clampInt(seconds, 1, maxWaitSecs)) * time.Second
}

// patchFromUpdate builds the patch as a generic map and runs it through
// stripForbidden even though updateIn has no forbidden field. The rule is
// that the patch is what gets sanitized, so a future caller that builds one
// from a VM object it read back is covered.
func patchFromUpdate(in updateIn) map[string]any {
	p := map[string]any{}
	if in.RAMMB != 0 {
		p["ram"] = in.RAMMB
	}
	if in.CPUs != 0 {
		p["cpus"] = in.CPUs
	}
	if in.SSHPort != 0 {
		p["ssh_port"] = in.SSHPort
	}
	if in.Disk != "" {
		p["disk"] = in.Disk
	}
	if in.Recipes != nil {
		p["recipes"] = in.Recipes
	}
	return stripForbidden(p)
}

func (s *srv) registerVM(server *mcp.Server) {
	register(server, "create", classMutate,
		"Create a new VM from a catalog image, without starting it. Only catalog image ids are accepted, see list_images; a bring-your-own image path, a console password and a host share cannot be set through this tool. Memory is ram_mb and is in MEGABYTES. Reversible: destroy deletes the VM. Mutating: it creates a VM directory and a disk under the stoat data root.",
		func(ctx context.Context, in createIn) (wire.VM, error) {
			name, err := checkVMName(in.Name)
			if err != nil {
				return wire.VM{}, err
			}
			image, err := checkImageID(in.Image)
			if err != nil {
				return wire.VM{}, err
			}
			if err := checkFlagFree(in.Recipes, "recipes"); err != nil {
				return wire.VM{}, err
			}
			access := in.AgentAccess
			if access == "" {
				access = LevelManage.String()
			}
			if _, err := ParseLevel(access); err != nil {
				return wire.VM{}, err
			}
			v, err := core.Create(core.Spec{
				Name: name, Image: image, OS: in.OS, Backend: in.Backend, Mode: in.Mode,
				RAM: in.RAMMB, CPUs: in.CPUs, Disk: in.Disk, Recipes: in.Recipes,
				AgentAccess: access,
			})
			if err != nil {
				return wire.VM{}, err
			}
			return wire.FromVM(v, core.GraphicalSession()), nil
		})

	register(server, "start", classMutate,
		"Start a VM, which boots qemu. Reversible with stop. Mutating: it consumes host CPU and RAM and, once running, a forwarded ssh port.",
		s.byName(core.Start))

	register(server, "stop", classMutate,
		"Stop a running VM gracefully. Reversible with start. Mutating: it shuts qemu down, and it refuses when the VM is not running.",
		s.byName(core.Stop))

	register(server, "destroy", classDestructive,
		"Permanently delete a VM's directory and its disk. It refuses while the VM is running. This is NOT reversible: there is no undo, and a snapshot taken before the deletion goes with it.",
		s.byName(core.Destroy))

	register(server, "update", classMutate,
		"Change a stopped VM's RAM, CPU count, ssh port, disk size (grow only), recipe list, recipe params or recipe secrets, or lower its agent access level. Only the fields you pass change. A share cannot be set through this tool. Raising agent_access is refused here; raise it from the CLI or the TUI. Mutating; most fields take effect at the VM's next start.",
		func(ctx context.Context, in updateIn) (wire.VM, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.VM{}, err
			}
			patch, err := corePatch(name, in)
			// A future logging or tracing middleware reads req.GetParams(),
			// not this local in, so clearing it here does not by itself stop
			// a leak; corePatch has already copied what it needs into patch.
			in.Secrets = nil
			if err != nil {
				return wire.VM{}, err
			}
			v, err := core.Update(name, patch)
			if err != nil {
				return wire.VM{}, err
			}
			return wire.FromVM(v, core.GraphicalSession()), nil
		})

	register(server, "clone", classMutate,
		"Copy a VM: a fresh overlay disk and a fresh ssh port, but NOT the source's port forwards. It refuses a running source. Reversible with destroy on the clone. Mutating: it creates a new VM.",
		func(ctx context.Context, in cloneIn) (wire.VM, error) {
			src, err := checkVMName(in.Source)
			if err != nil {
				return wire.VM{}, err
			}
			dst, err := checkVMName(in.Name)
			if err != nil {
				return wire.VM{}, err
			}
			v, err := core.Clone(src, dst)
			if err != nil {
				return wire.VM{}, err
			}
			return wire.FromVM(v, core.GraphicalSession()), nil
		})

	register(server, "snapshot", classMutate,
		"Save a disk snapshot of a VM under a tag. It needs a disk to snapshot, so a live-mode VM refuses. Reversible: restore rolls back to it, and a person can remove the tag with stoat snapshot --delete. Mutating: it writes a new qemu snapshot.",
		func(ctx context.Context, in snapshotIn) (wire.SnapshotList, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.SnapshotList{}, err
			}
			if err := core.TakeSnapshot(name, in.Tag); err != nil {
				return wire.SnapshotList{}, err
			}
			ss, err := core.Snapshots(name)
			if err != nil {
				return wire.SnapshotList{}, err
			}
			return wire.SnapshotList{Snapshots: wire.FromSnapshots(ss)}, nil
		})

	register(server, "restore", classDestructive,
		"Roll a VM's disk back to a saved snapshot tag and discard everything written since. Destructive: it is reversible only when another snapshot was taken after the one you restore to.",
		func(ctx context.Context, in snapshotIn) (wire.SnapshotList, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.SnapshotList{}, err
			}
			if err := core.Restore(name, in.Tag); err != nil {
				return wire.SnapshotList{}, err
			}
			ss, err := core.Snapshots(name)
			if err != nil {
				return wire.SnapshotList{}, err
			}
			return wire.SnapshotList{Snapshots: wire.FromSnapshots(ss)}, nil
		})

	register(server, "forward", classMutate,
		"Show, set or clear a VM's host:guest port forwards. With no pairs and clear=false it only shows the current forwards. Setting or clearing on a running VM saves the change, and the change takes effect at the VM's next start. Mutating when you pass pairs or clear=true.",
		func(ctx context.Context, in forwardIn) (wire.ForwardList, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.ForwardList{}, err
			}
			if err := checkFlagFree(in.Pairs, "pairs"); err != nil {
				return wire.ForwardList{}, err
			}
			fwds, err := core.ParseForwards(in.Pairs)
			if err != nil {
				return wire.ForwardList{}, err
			}
			if in.Clear {
				fwds = []core.PortForward{}
			}
			if in.Clear || len(in.Pairs) > 0 {
				if _, err := core.Forward(name, fwds); err != nil {
					return wire.ForwardList{}, err
				}
			}
			v, err := core.Get(name)
			if err != nil {
				return wire.ForwardList{}, err
			}
			return wire.ForwardList{Forwards: wire.FromPortForwards(v.Forwards)}, nil
		})

	register(server, "wait", classMutate,
		"Block until a VM reaches a state: reachable when sshd answers, applied when the most recent recipe run finished, or stopped when qemu is gone. With healthy=true it also waits for every applied recipe's health check to pass. The bound is timeout_seconds, a plain count of seconds and not a duration string, capped at 600. A state the VM can never reach fails at once rather than waiting out the timeout. Mutating only in that it blocks the caller.",
		func(ctx context.Context, in waitIn) (wire.WaitResult, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.WaitResult{}, err
			}
			// core.Wait takes an Until, not an options struct. healthy and
			// until are two different waits, so passing both is refused.
			until := core.Until(in.Until)
			if in.Until == "" {
				until = core.UntilReachable
			}
			if in.Healthy {
				if in.Until != "" {
					return wire.WaitResult{}, fmt.Errorf("healthy and until are two different waits; pass one")
				}
				until = core.UntilHealthy
			}
			timeout := waitTimeout(in.TimeoutSeconds)
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			if err := core.Wait(ctx, name, until); err != nil {
				return wire.WaitResult{}, err
			}
			return wire.WaitResult{VM: name, Until: string(until), Healthy: in.Healthy}, nil
		})

	register(server, "prune", classDestructive,
		"Report stale files stoat can clean up: partial downloads always, broken VM directories with broken=true, and orphaned images with images=true. It is a dry run by default and only reports; pass apply=true to delete, which is NOT reversible for whatever it removes.",
		func(ctx context.Context, in pruneIn) (wire.PruneList, error) {
			items, err := core.Prune(core.PruneOpts{DryRun: !in.Apply, Broken: in.Broken, Images: in.Images})
			if err != nil {
				return wire.PruneList{}, err
			}
			return wire.PruneList{Items: wire.FromPruneItems(items), DryRun: !in.Apply}, nil
		})

	register(server, "apply_recipes", classExec,
		"Run a VM's own configured recipes over ssh, or a named subset of them. Call plan_recipes first to see what this would do. A recipe body is arbitrary guest code, so this needs agent_access manage or higher. Mutating: it runs recipe scripts inside the guest, and that is reversible only to whatever extent the recipe itself is. It reaches outside this process.",
		func(ctx context.Context, in applyIn) (wire.ApplyResult, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.ApplyResult{}, err
			}
			if err := requireAccess(name, LevelManage); err != nil {
				return wire.ApplyResult{}, err
			}
			if err := checkFlagFree(in.Only, "only"); err != nil {
				return wire.ApplyResult{}, err
			}
			if err := core.Apply(ctx, name, core.ApplyOpts{Only: in.Only}); err != nil {
				return wire.ApplyResult{}, err
			}
			v, err := core.Get(name)
			if err != nil {
				return wire.ApplyResult{}, err
			}
			return wire.FromApplyResult(v), nil
		})
}

// byName builds a handler for a tool whose only input is a VM name and
// whose result is the VM afterwards. start, stop and destroy differ only by
// the core call.
func (s *srv) byName(fn func(string) error) func(context.Context, vmIn) (wire.VM, error) {
	return func(ctx context.Context, in vmIn) (wire.VM, error) {
		name, err := checkVMName(in.VM)
		if err != nil {
			return wire.VM{}, err
		}
		if err := fn(name); err != nil {
			return wire.VM{}, err
		}
		v, err := core.Get(name)
		if err != nil {
			// destroy removes the VM, so a not-found read afterwards is the
			// expected outcome and not a failure of the tool.
			return wire.VM{Name: name, State: "gone"}, nil
		}
		return wire.FromVM(v, core.GraphicalSession()), nil
	}
}

// corePatch converts the tool input to core.Patch. agent_access is checked
// against the VM's current level here rather than in core, because core is a
// library the CLI and TUI call with the authority to raise it.
func corePatch(name string, in updateIn) (core.Patch, error) {
	generic := patchFromUpdate(in)
	p := core.Patch{}
	if v, ok := generic["ram"].(int); ok {
		p.RAM = &v
	}
	if v, ok := generic["cpus"].(int); ok {
		p.CPUs = &v
	}
	if v, ok := generic["ssh_port"].(int); ok {
		p.SSHPort = &v
	}
	if v, ok := generic["disk"].(string); ok {
		p.Disk = &v
	}
	if v, ok := generic["recipes"].([]string); ok {
		p.Recipes = &v
	}
	for _, params := range in.Params {
		for k := range params {
			if _, err := checkParamName(k); err != nil {
				return core.Patch{}, err
			}
		}
	}
	for _, params := range in.Secrets {
		for k := range params {
			if _, err := checkParamName(k); err != nil {
				return core.Patch{}, err
			}
		}
	}
	p.SetParams = in.Params
	p.Secrets = config.Secrets(in.Secrets)
	if in.AgentAccess != "" {
		want, err := ParseLevel(in.AgentAccess)
		if err != nil {
			return core.Patch{}, err
		}
		cur, err := currentLevel(name)
		if err != nil {
			return core.Patch{}, err
		}
		if want.rank() > cur.rank() {
			return core.Patch{}, fmt.Errorf("vm %q has agent_access = %s; this tool may only lower it, and raising it is a CLI or TUI action", name, cur)
		}
		access := want.String()
		p.AgentAccess = &access
	}
	return p, nil
}
