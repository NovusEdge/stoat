package mcpsrv

import (
	"bufio"
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

type emptyIn struct{}

type vmIn struct {
	VM string `json:"vm" jsonschema:"name of the VM"`
}

type capabilitiesIn struct {
	VM string `json:"vm,omitempty" jsonschema:"optional VM name; omit for host scope"`
}

type listRecipesIn struct {
	OS      string `json:"os,omitempty" jsonschema:"only recipes applicable to this guest OS"`
	Backend string `json:"backend,omitempty" jsonschema:"only recipes applicable to this backend"`
}

type checkRecipesIn struct {
	Recipes []string `json:"recipes" jsonschema:"recipe names to check"`
	OS      string   `json:"os" jsonschema:"guest OS to check against"`
	Backend string   `json:"backend,omitempty" jsonschema:"backend to check against"`
}

type logsIn struct {
	VM    string `json:"vm" jsonschema:"name of the VM"`
	Which string `json:"which,omitempty" jsonschema:"console for the qemu console log, apply for the most recent recipe apply log"`
	N     int    `json:"n,omitempty" jsonschema:"number of lines to tail, capped at 2000"`
}

type planRecipesIn struct {
	VM   string   `json:"vm" jsonschema:"name of the VM"`
	Only []string `json:"only,omitempty" jsonschema:"subset of the VM's own recipes"`
}

type nameIn struct {
	Name string `json:"name" jsonschema:"name to look up"`
}

type searchIn struct {
	Term string `json:"term" jsonschema:"text to match against recipe names and descriptions"`
}

func (s *srv) registerRead(server *mcp.Server) {
	register(server, "list_vms", classRead,
		"List every VM stoat manages, one entry per VM: name, OS, mode, state, resources, disk, share, ssh port, agent access level, recipes, and port forwards. A VM whose vm.toml failed to parse is listed with state broken and an error message rather than hidden. Read-only: it touches no VM and changes nothing on the host.",
		func(ctx context.Context, _ emptyIn) (wire.VMList, error) {
			vms, err := core.List()
			if err != nil {
				return wire.VMList{}, err
			}
			core.AttachKeys(vms, s.proj)
			return wire.VMList{VMs: wire.FromVMs(vms, core.GraphicalSession())}, nil
		})

	register(server, "vm_status", classRead,
		"Show one VM's full status: OS, mode, backend, state, CPUs, RAM, disk, share, ssh port and user, agent access level, applied recipes with their health and outputs, and port forwards. Read-only: it touches no VM and changes nothing on the host.",
		func(ctx context.Context, in vmIn) (wire.VMStatus, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.VMStatus{}, err
			}
			v, err := core.Get(name)
			if err != nil {
				return wire.VMStatus{}, err
			}
			return wire.FromVMStatus(v, core.GraphicalSession()), nil
		})

	register(server, "list_images", classRead,
		"List what stoat can build a VM from: the catalog of known images, plus anything already downloaded locally. Read-only: it downloads nothing and changes nothing on the host.",
		func(ctx context.Context, _ emptyIn) (wire.ImageList, error) {
			imgs, err := core.Images()
			if err != nil {
				return wire.ImageList{}, err
			}
			return wire.ImageList{Images: wire.FromCatalogImages(imgs)}, nil
		})

	register(server, "list_recipes", classRead,
		"List recipes stoat knows about, optionally filtered to the ones applicable to a guest OS or a backend. It reads the recipe directories only, and runs nothing in any VM. Read-only.",
		func(ctx context.Context, in listRecipesIn) (wire.RecipeCatalog, error) {
			rs, err := core.Recipes(core.RecipeFilter{OS: in.OS, Backend: in.Backend})
			if err != nil {
				return wire.RecipeCatalog{}, err
			}
			return wire.RecipeCatalog{Recipes: wire.FromRecipes(rs)}, nil
		})

	register(server, "check_recipes", classRead,
		"Report, for each named recipe, why it would not apply to a given guest OS and backend. An empty issue list means every named recipe would apply. It runs nothing and touches no VM. Read-only.",
		func(ctx context.Context, in checkRecipesIn) (wire.RecipeIssueList, error) {
			if err := checkFlagFree(in.Recipes, "recipes"); err != nil {
				return wire.RecipeIssueList{}, err
			}
			issues, err := core.CheckRecipes(in.OS, in.Backend, in.Recipes)
			if err != nil {
				return wire.RecipeIssueList{}, err
			}
			return wire.RecipeIssueList{Issues: wire.FromRecipeIssues(issues)}, nil
		})

	register(server, "logs", classRead,
		"Tail one VM's log: its qemu console output by default, or its most recent recipe apply log. It is always scoped to one named VM, and there is no way to read stoat's own global log through this tool. The line count is capped at 2000. Read-only.",
		func(ctx context.Context, in logsIn) (wire.LogTail, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.LogTail{}, err
			}
			which := core.Which(in.Which)
			if in.Which == "" {
				which = core.WhichConsole
			}
			rc, err := core.Logs(name, which)
			if err != nil {
				return wire.LogTail{}, err
			}
			defer func() { _ = rc.Close() }()
			return tailLines(rc, clampInt(in.N, 1, maxLogLines))
		})

	register(server, "doctor", classRead,
		"Check host prerequisites: qemu and KVM, qemu-img, ssh, xorriso, git and /dev/kvm. The check always succeeds; an unhealthy host is reported in the result rather than raised as a failure. Read-only: it changes nothing on the host.",
		func(ctx context.Context, _ emptyIn) (wire.DoctorReport, error) {
			return wire.FromDoctor(core.Doctor()), nil
		})

	register(server, "capabilities", classRead,
		"Read host checks and stored VM metadata to report current capabilities, MCP access limits, and unavailable fork or continuation proposals. It does not start or connect to a VM. Read-only.",
		func(ctx context.Context, in capabilitiesIn) (wire.Capabilities, error) {
			var target *capabilities.Target
			if in.VM != "" {
				name, err := checkVMName(in.VM)
				if err != nil {
					return wire.Capabilities{}, err
				}
				loaded, err := capabilities.LoadTarget(name)
				if err != nil {
					return wire.Capabilities{}, err
				}
				target = &loaded
			}
			projectState := "absent"
			switch {
			case s.projErr != nil:
				projectState = "unknown"
			case s.proj != nil:
				projectState = "available"
			}
			return wire.Capabilities(capabilities.Build(capabilities.Input{
				Version: s.opts.Version, ProjectState: projectState,
				HostChecks: core.Doctor(), Target: target,
			})), nil
		})

	register(server, "plan_recipes", classRead,
		"Report what apply_recipes would do to a VM, without running anything: one entry per recipe with run or skip and the reason. It is computed on the host, so it works on a stopped VM. Call it before apply_recipes. Read-only.",
		func(ctx context.Context, in planRecipesIn) (wire.ApplyPlanList, error) {
			name, err := checkVMName(in.VM)
			if err != nil {
				return wire.ApplyPlanList{}, err
			}
			if err := checkFlagFree(in.Only, "only"); err != nil {
				return wire.ApplyPlanList{}, err
			}
			plans, err := core.PlanApply(name, core.ApplyOpts{Only: in.Only})
			if err != nil {
				return wire.ApplyPlanList{}, err
			}
			return wire.ApplyPlanList{Plan: wire.FromApplyPlans(plans)}, nil
		})

	register(server, "list_guests", classRead,
		"List every guest OS definition stoat knows: name, init system, package manager, default backend and whether the definition is bundled, a user file, or a user file merged over a bundled one. It reads the guest definitions only. Read-only.",
		func(ctx context.Context, _ emptyIn) (wire.GuestList, error) {
			// core.Guests returns no error: the bundled set is embedded and
			// a user file that fails to parse was already refused at startup.
			return wire.GuestList{Guests: wire.FromGuests(core.Guests())}, nil
		})

	register(server, "guest_info", classRead,
		"Show one guest OS definition in full: init system, shell, escalate argv, capabilities, aliases, seed packages, the package manager verbs, the service verbs and the per-backend tables. Use it to learn what pkg_install and svc will run on a VM before you call them. Read-only.",
		func(ctx context.Context, in nameIn) (wire.Guest, error) {
			g, err := core.Guest(in.Name)
			if err != nil {
				return wire.Guest{}, err
			}
			return wire.FromGuest(g), nil
		})

	register(server, "recipe_schema", classRead,
		"Show one recipe's contract: its params with type, default and help, its declared outputs, and its health check. Read it before update sets params on a VM. Read-only.",
		func(ctx context.Context, in nameIn) (wire.RecipeSchema, error) {
			// core.EnsureRecipes installs the bundled set: mcpsrv has no
			// startup entrypoint of its own to do this once, unlike the
			// CLI's dispatch loop.
			if err := core.EnsureRecipes(); err != nil {
				return wire.RecipeSchema{}, err
			}
			r, err := core.RecipeShow(in.Name)
			if err != nil {
				return wire.RecipeSchema{}, err
			}
			return wire.FromRecipeSchema(r), nil
		})

	register(server, "search_recipes", classRead,
		"Search the recipe index by name and description. It refreshes the local index copy when that copy is older than 24 hours. It installs nothing. Read-only.",
		func(ctx context.Context, in searchIn) (wire.RecipeSearch, error) {
			if err := checkFlagFree([]string{in.Term}, "term"); err != nil {
				return wire.RecipeSearch{}, err
			}
			rs, err := core.SearchRecipes(in.Term)
			if err != nil {
				return wire.RecipeSearch{}, err
			}
			return wire.RecipeSearch{Recipes: wire.FromIndexEntries(rs)}, nil
		})
}

// tailLines returns the last n lines of r. core.Logs streams the whole file,
// and the tail comes back in one response, so clamping beats handing an
// agent a payload it cannot read.
func tailLines(r io.Reader, n int) (wire.LogTail, error) {
	ring := make([]string, 0, n)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return wire.LogTail{}, err
	}
	return wire.LogTail{Lines: ring}, nil
}
