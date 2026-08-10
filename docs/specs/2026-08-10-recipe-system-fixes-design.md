# Recipe System Fixes

Status: **draft**

## Summary

This spec addresses six architectural issues in the recipe system. Each fix can be implemented independently, though the order below minimizes rework.

## Priority Order

1. Delete v1 metadata parser (foundational)
2. Add dependency ordering
3. Add dry-run
4. Implement stage field
5. Document single-reboot behavior
6. Add cloudinit post-boot path

---

## 1. Delete v1 Metadata Parser

**Can implement independently:** Yes

### Problem

Two metadata systems coexist: `metadata.go` parses `# stoat: key value` comment front matter from v1 flat files, `manifest.go` parses `recipe.toml` from v2 directories. Both define `OS`, `Requires`, and capability checking with different type shapes and resolution logic. A bugfix to capability resolution must land in two places.

### Solution

Delete the v1 path. Make `Manifest` the single source of truth.

### Files to Delete

- `internal/recipes/metadata.go`
- `internal/recipes/metadata_test.go`

### Files to Modify

**`internal/core/apply.go`:**
- Remove `ReadMetadata` calls
- Use `ManifestFor` exclusively

**`internal/recipes/recipes.go`:**
- Remove v1 fallback paths in `List`, `ScriptBody`
- `Read(name)` becomes unused; delete or deprecate

### Migration

User-created `.sh` files in `~/.stoat/recipes/` become unrecognized after this change.

Add a startup warning if flat files exist outside `.v1-removed/`:
```
Warning: Found legacy recipe files in ~/.stoat/recipes/. Convert to v2 format. See docs/writing-recipes.md.
```

The `.v1-removed/` attic already holds swept files from the v1→v2 migration. This change completes the deprecation period.

### Result

`MatchesVM` becomes the only capability checker. `UnsupportedReason` (v1 version) goes away.

---

## 2. Dependency Ordering

**Can implement independently:** Yes (after v1 deletion for cleaner code, but not required)

### Problem

`v.Recipes` is a flat list run in array order. Recipes cannot declare "run after X". If devtools assumes Docker is present, the user must manually order them correctly.

### Manifest Change

```toml
name = "devtools"
depends = ["docker"]
```

`depends` is optional, defaults to empty. Each entry is a recipe name.

### Validation

**In `ParseManifest`:**
- Validate `depends` entries are strings
- Unknown recipe names are caught later at add-recipe time

**In `CheckRecipes` / TUI add-recipe flow:**
- Build a dependency graph from all manifests in `v.Recipes` + the new recipe
- Detect cycles via DFS
- Error with the cycle path if found: `"cycle detected: devtools -> docker -> devtools"`
- Cycle detection runs when recipes are added to a VM

### Topo-Sort

**In `filterByRunMode`:**
- Before filtering by run mode, topo-sort `targets` so dependencies come first
- A recipe with `depends = ["docker"]` runs after docker regardless of array order in `v.Recipes`
- Kahn's algorithm or DFS-based sort; either works for small N

### TUI Behavior

User adds "devtools" which depends on "docker":
- If docker is already in `v.Recipes`: proceed
- If docker is missing: auto-add it, show message: `"Added docker (required by devtools)"`

### CLI Behavior

`stoat vm create --recipes devtools` errors if docker isn't also specified:
```
Error: devtools depends on docker; add it to the recipe list
```

The CLI does not auto-add. Scripts must be explicit.

---

## 3. Dry-Run

**Can implement independently:** Yes

### Problem

`Apply()` runs scripts directly. There's no way to preview what would run without running it. The filtering logic in `filterByRunMode` returns a trimmed list; a caller that wants to preview must duplicate that logic.

### Solution

Add `stoat apply --dry-run` and expose the filtering result.

### CLI Output

**Human-readable (default):**
```
xfce (pending, never applied)
docker (pending, never applied)
tailscale (skip, already applied at v1.0.0)
```

**Structured (`--json`):**
```json
[
  {"name": "xfce", "action": "run", "reason": "never applied"},
  {"name": "docker", "action": "run", "reason": "never applied"},
  {"name": "tailscale", "action": "skip", "reason": "already applied", "version": "1.0.0"}
]
```

### Implementation

**New function in `internal/recipes/` or `internal/core/`:**
```go
type ApplyPlan struct {
    Name    string
    Action  string // "run" | "skip"
    Reason  string
    Version string // from v.Applied, if present
}

func PlanApply(v *config.VM, opts ApplyOpts) ([]ApplyPlan, error)
```

`PlanApply` calls the same filtering logic as `applyLocked` but returns the plan instead of executing.

**CLI flag:**
- `stoat apply --dry-run` calls `PlanApply`, prints result, exits
- `stoat apply --dry-run --json` prints JSON

**TUI:**
- Before confirming apply, show the plan in a preview pane
- Use the same `PlanApply` function

---

## 4. Implement Stage Field

**Can implement independently:** Yes

### Problem

`stage = "install"` parses and validates, but install-stage recipe bodies are never executed. The Alpine disk-mode answerfile is generated from VM config, not from recipes. The field exists in the schema with no effect.

### Use Case

BYO ISO support needs install-stage hooks for custom partitioning, bootloader config, and installer automation.

### Solution

Execute install-stage recipes before first boot.

### Execution by Backend

**apkovl backend (disk mode):**
- Install-stage recipes bake into `/etc/local.d/` alongside `stoat-install.start`
- They run in dependency order before `setup-alpine`
- Naming: `00-<recipe>.start`, `01-<recipe>.start`, etc.

**cloudinit backend:**
- Install-stage recipes go into `bootcmd` with a guard file check
- Pattern: `[ -f /var/lib/stoat/.installed/<recipe> ] || { /var/lib/stoat/recipes/<recipe>.sh && touch /var/lib/stoat/.installed/<recipe>; }`
- This runs once even though `bootcmd` executes every boot

**ssh backend:**
- Install stage is not applicable; SSH implies the system is already installed
- Validate at add-recipe time: error if an install-stage recipe is added to an ssh-backend VM

### Manifest

No schema change needed. `stage` already accepts `"install"` | `"provision"`.

### State Tracking

Install-stage recipes are tracked in `v.Applied` like provision-stage recipes. The "already applied" check uses the same hash comparison.

---

## 5. Single Reboot Behavior

**Can implement independently:** Yes (documentation only)

### Current Behavior

`reboot = true` in the manifest triggers one reboot after all recipes finish. The first recipe that declares `reboot = true` names the reboot in the log. Subsequent `reboot = true` recipes don't trigger additional reboots.

### Scope

Keep this behavior. Document it clearly.

### Documentation Update

In `docs/recipe-spec-v2.md`, add a section:

```markdown
## Reboot Handling

A recipe can declare `reboot = true` to indicate the guest needs a reboot
for changes to take effect (e.g., switching init systems, loading new
kernel modules).

When one or more recipes in a run declare `reboot = true`, stoat reboots
the guest once after all recipes complete. The reboot is not per-recipe;
it happens at the end of the apply run.

For disk-mode VMs, the reboot persists changes. For live-mode VMs, root
is tmpfs and the reboot wipes everything; live recipes that need a
"reboot" should restart their session in place instead.
```

### Future Extension

Per-recipe reboots (reboot after recipe A, then run recipe B) could be added later. This would require a `reboot = "after"` vs `reboot = "end"` distinction. Out of scope for this spec.

---

## 6. Cloudinit Post-Boot Path

**Can implement independently:** Yes (largest scope item)

### Problem

The cloudinit backend bakes provision-stage recipes into `write_files + runcmd` at VM creation time. `Apply()` returns `ErrAppliedAtBoot` and refuses to run. This means:

- `v.Applied` is never populated for cloudinit VMs
- `run = "once"` has no effect; cloud-init runs once by definition
- `run = "always"` cannot re-run recipes post-boot
- There's no way to add recipes to a running cloudinit VM

### Solution

Remove `ErrAppliedAtBoot`. Allow `Apply()` to work on cloudinit VMs via SSH after first boot. Track what cloud-init ran via marker files.

### Marker Files

**At VM creation (in cloudinit backend's seed generation):**

Change the runcmd wrapper from:
```yaml
runcmd:
  - /var/lib/stoat/recipes/xfce.sh
```

To:
```yaml
runcmd:
  - /var/lib/stoat/recipes/xfce.sh && mkdir -p /var/lib/stoat/.applied && touch /var/lib/stoat/.applied/xfce
```

Each recipe writes a marker file on success.

### Apply Behavior

**In `applyLocked` (after removing the `ErrAppliedAtBoot` check):**

1. If backend is cloudinit and `v.Applied` is empty, SSH in and read `/var/lib/stoat/.applied/`
2. Populate `v.Applied` with entries for each marker file found (version from manifest, hash from current script, timestamp now)
3. Save `v` to persist the discovered state
4. Continue with normal filtering via `filterByRunMode`

**Fallback:** If `/var/lib/stoat/.applied/` doesn't exist (old VM created before this feature, or cloud-init failed entirely), treat all recipes as pending. The first `Apply()` re-runs everything once; after that, state is tracked correctly. Recipes are idempotent, so re-running is safe.

### Files to Modify

**`internal/core/apply.go`:**
- Remove the `backend.For(v).Name() == "cloudinit"` check that returns `ErrAppliedAtBoot`
- Add `discoverCloudInitApplied(ctx, v)` call before `filterByRunMode`

**`internal/backend/cloudinit/cloudinit.go`:**
- Modify `Prepare()` to wrap each recipe with the marker-writing suffix

### Result

Cloudinit VMs behave like other VMs after first boot:
- `Apply()` works over SSH
- `v.Applied` tracks what ran
- `run = "once"` skips already-applied recipes
- `run = "always"` re-runs every time
- New recipes can be added and applied post-creation

---

## Testing Strategy

### Unit Tests

- `TestParseManifestDependsField` — validates depends parsing
- `TestCycleDetection` — catches A→B→A cycles
- `TestTopoSort` — orders recipes by dependencies
- `TestPlanApply` — returns correct plan without executing
- `TestCloudInitMarkerDiscovery` — reads marker files, populates Applied

### Integration Tests

- Create a VM with depends, verify execution order
- `--dry-run` outputs correct plan
- cloudinit VM: apply after first boot, verify state tracking

### Manual Tests

- TUI: add recipe with missing dep, verify auto-add message
- CLI: `--recipes devtools` without docker, verify error
- cloudinit: create VM, wait for boot, `stoat apply`, check `v.Applied`

---

## Implementation Notes

### Order Matters

The fixes can land independently, but this order minimizes conflicts:

1. **Delete v1 metadata** first — every other change touches fewer code paths
2. **Depends** — adds a manifest field, touches `filterByRunMode`
3. **Dry-run** — extracts logic from `applyLocked`, no schema change
4. **Stage field** — touches backend Prepare functions
5. **Reboot docs** — pure documentation
6. **Cloudinit post-boot** — largest change, benefits from cleaner codebase

### Backwards Compatibility

- v1 flat-file recipes stop working; users see a warning and must migrate
- Existing v2 recipes without `depends` keep working (empty deps = no ordering constraint)
- Existing cloudinit VMs get their `v.Applied` populated on first post-boot `Apply()`
