# Recipe System Fixes

Status: **draft**

## Summary

This spec addresses six architectural issues in the recipe system. Items 1 and the cloudinit v2 conversion are coupled; the rest can land independently.

## Priority Order

1. Delete v1 metadata parser + wire cloudinit to v2 scripts (coupled)
2. Add dependency ordering
3. Cloudinit post-boot path (markers + remove ErrAppliedAtBoot)
4. Add dry-run
5. Document single-reboot behavior
6. Stage field validation (reject until BYO ISO lands)

---

## 1. Delete v1 Metadata Parser + Cloudinit v2 Conversion

**Can implement independently:** No — cloudinit's `Prepare()` and `clone.go` call `recipes.Read()` on v2 directory names, which fails. These must be fixed together.

### Problem

Two metadata systems coexist: `metadata.go` parses `# stoat: key value` comment front matter from v1 flat files, `manifest.go` parses `recipe.toml` from v2 directories. Both define `OS`, `Requires`, and capability checking with different type shapes and resolution logic.

Additionally, the cloudinit backend is already broken for v2 recipes. `cloudinit.Prepare()` calls `recipes.Read("xfce")`, which does `os.ReadFile` on a directory and errors. The v2-aware `cloudinit.WrapScripts()` exists but has zero non-test callers.

### Solution

Delete the v1 path, wire cloudinit to v2 scripts, and make `Manifest` the single source of truth.

### Files to Delete

- `internal/recipes/metadata.go`
- `internal/recipes/metadata_test.go`

### Files to Modify

**`internal/core/apply.go`:**
- Remove `ReadMetadata` calls
- Use `ManifestFor` exclusively
- Remove cloud-fragment branches in `recipeIssueReason` (no longer applicable)

**`internal/recipes/recipes.go`:**
- Remove v1 fallback paths in `List`, `ScriptBody`
- Delete `Read(name)` — no longer used

**`internal/backend/cloudinit/cloudinit.go`:**
- Replace `recipes.Read()` calls with `recipes.ManifestFor()` + `ScriptContent()`
- Wire `WrapScripts()` into `Prepare()`

**`internal/core/clone.go`:**
- Same conversion: `ManifestFor()` + `ScriptContent()` instead of `Read()`

### Migration

`sweepV1()` already moves flat files into `.v1-removed/` at Install time. User-created flat files are swept before any warning could fire.

To catch user recipes being swept: log a warning *when sweepV1 moves a file* that the manifest doesn't recognize as stoat's own copy:
```
Warning: Moved legacy recipe <name> to .v1-removed/. Convert to v2 format. See docs/writing-recipes.md.
```

### Result

`MatchesVM` becomes the only capability checker. `UnsupportedReason` (v1 version) goes away. Cloudinit backend works with v2 recipes.

---

## 2. Dependency Ordering

**Can implement independently:** Yes

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
- Unknown recipe names are caught at add-recipe time

**In `CheckRecipes` / TUI add-recipe flow:**
- Build a dependency graph from all manifests in `v.Recipes` + the new recipe
- Detect cycles via DFS
- Error with the cycle path if found: `"cycle detected: devtools -> docker -> devtools"`
- Auto-added dependencies must pass `MatchesVM` for the target VM's OS

### Topo-Sort

**In `filterByRunMode`:**
- Before filtering by run mode, topo-sort `targets` so dependencies come first
- A recipe with `depends = ["docker"]` runs after docker regardless of array order in `v.Recipes`
- Kahn's algorithm or DFS-based sort; either works for small N
- **Cycle detection here too**: user can edit manifests on disk after add-time. Error if a cycle appears; do not silently fall back to array order.

### Dependency Satisfaction Rules

A dependency is satisfied when:
- The dependency recipe ran earlier in this apply run, OR
- The dependency recipe was already applied (`v.Applied` has an entry for it)

A dependency is **not** satisfied when:
- The dependency is `run = "manual"` and was never applied — error: `"devtools depends on docker, which has never been applied (run = manual)"`
- The dependency is filtered out by `ApplyOpts.Only` and was never applied — error: `"devtools depends on docker; add it to --recipe or apply it first"`

### TUI Behavior

User adds "devtools" which depends on "docker":
- If docker is already in `v.Recipes`: proceed
- If docker is missing: run `CheckRecipes` on docker for this VM's OS. If it passes, auto-add and show: `"Added docker (required by devtools)"`. If it fails, error with the reason.

### CLI Behavior

`stoat vm create --recipes devtools` errors if docker isn't also specified:
```
Error: devtools depends on docker; add it to the recipe list
```

The CLI does not auto-add. Scripts must be explicit.

---

## 3. Cloudinit Post-Boot Path

**Can implement independently:** Yes (after item 1)

### Problem

After item 1, cloudinit generates seeds with v2 scripts. `Apply()` still returns `ErrAppliedAtBoot` and refuses to run. This means:

- `v.Applied` is never populated for cloudinit VMs
- `run = "once"` has no effect
- `run = "always"` cannot re-run recipes post-boot
- No way to add recipes to a running cloudinit VM

### Solution

Remove `ErrAppliedAtBoot`. Allow `Apply()` to work on cloudinit VMs via SSH after first boot. Track what cloud-init ran via marker files.

### Marker Files

**At VM creation (in cloudinit backend's seed generation):**

Wrap each recipe's runcmd entry:
```yaml
runcmd:
  - /var/lib/stoat/recipes/xfce.sh && mkdir -p /var/lib/stoat/.applied && touch /var/lib/stoat/.applied/xfce
```

Each recipe writes a marker file on success.

### Apply Behavior

**In `applyLocked` (after removing the `ErrAppliedAtBoot` check):**

1. If backend is cloudinit and `v.Applied` is empty, SSH in and read `/var/lib/stoat/.applied/`
2. Populate `v.Applied` with entries for each marker file found
3. Save `v` to persist the discovered state
4. Continue with normal filtering via `filterByRunMode`

**Hash note:** The hash stored in `v.Applied` comes from the *current* script on disk, which may differ from what cloud-init ran at creation time. This is benign: recipes are idempotent, and a changed script triggers a re-run on the next `Apply()` anyway.

**Fallback:** If `/var/lib/stoat/.applied/` doesn't exist (old VM created before this feature, or cloud-init failed entirely), treat all recipes as pending. The first `Apply()` re-runs everything once; after that, state is tracked correctly.

### Files to Modify

**`internal/core/apply.go`:**
- Remove the `backend.For(v).Name() == "cloudinit"` check that returns `ErrAppliedAtBoot`
- Add `discoverCloudInitApplied(ctx, v)` call before `filterByRunMode`

**`internal/cli/wire/errors.go`:**
- Remove `CodeAppliedAtBoot` mapping

**`internal/tui/provision.go`:**
- Remove `ErrAppliedAtBoot` checks (two locations)

**`internal/backend/cloudinit/cloudinit.go`:**
- Add marker-writing suffix to each recipe's runcmd entry

### Result

Cloudinit VMs behave like other VMs after first boot:
- `Apply()` works over SSH
- `v.Applied` tracks what ran
- `run = "once"` skips already-applied recipes
- `run = "always"` re-runs every time
- New recipes can be added and applied post-creation

---

## 4. Dry-Run

**Can implement independently:** Yes

### Problem

`Apply()` runs scripts directly. There's no way to preview what would run without running it.

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

**New function in `internal/core/`:**
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

**Works on stopped VMs:** The plan is computed host-side from manifests and `v.Applied`. The VM does not need to be running.

**CLI flag:**
- `stoat apply --dry-run` calls `PlanApply`, prints result, exits
- `stoat apply --dry-run --json` prints JSON

**TUI preview pane:** Out of scope for first cut. CLI-only delivers the value.

---

## 5. Single Reboot Behavior

**Can implement independently:** Yes (documentation only)

### Current Behavior

`reboot = true` in the manifest triggers one reboot after all recipes finish. The first recipe that declares `reboot = true` names the reboot in the log. Subsequent `reboot = true` recipes don't trigger additional reboots.

Reboot only fires for disk-mode VMs (`apply.go:185` checks `v.Mode == "disk"`).

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
the guest once after all recipes complete. The reboot happens at the end
of the apply run, after every recipe has finished.

Reboot applies to disk-mode VMs only. For live-mode VMs, root is tmpfs
and a reboot wipes everything. Live recipes that need a session restart
should restart in place instead (e.g., `kill -HUP 1`).
```

### Future Extension

Per-recipe reboots (reboot after recipe A, then run recipe B) could be added later. Out of scope for this spec.

---

## 6. Stage Field Validation

**Can implement independently:** Yes

### Problem

`stage = "install"` parses and validates, but install-stage recipe bodies are never executed. The field exists in the schema with no effect.

### Use Case

BYO ISO support will need install-stage hooks for custom partitioning, bootloader config, and installer automation. That feature does not exist yet.

### Solution

Reject `stage = "install"` at add-recipe time until BYO ISO lands.

### Implementation

**In `CheckRecipes` / TUI add-recipe flow:**
```go
if m.Stage == "install" {
    return fmt.Errorf("install-stage recipes are not yet supported")
}
```

### Why Not Implement Now

The cloudinit design has a bug: cloud-init's `bootcmd` runs *before* the `write_files` module, so the script file doesn't exist yet. `bootcmd` also runs before networking, so package installs fail. "Install stage" on a cloud image is conceptually unclear anyway — the system is already installed.

Building install-stage execution paths for three backends, each with different mechanisms, for a feature with no consumer yet is premature. When BYO ISO lands, the actual hooks needed will be clear.

### Future

When BYO ISO support is added, revisit this section. The implementation will likely differ from what we'd design today.

---

## Testing Strategy

### Unit Tests

- `TestParseManifestDependsField` — validates depends parsing
- `TestCycleDetection` — catches A→B→A cycles at add-time
- `TestCycleDetectionAtApplyTime` — catches cycles from edited manifests
- `TestTopoSort` — orders recipes by dependencies
- `TestDependencySatisfaction` — manual/never-applied deps error correctly
- `TestPlanApply` — returns correct plan without executing
- `TestPlanApplyStoppedVM` — works when VM is not running
- `TestCloudInitMarkerDiscovery` — reads marker files, populates Applied

### Integration Tests

- Create a VM with depends, verify execution order
- `--dry-run` outputs correct plan
- cloudinit VM: apply after first boot, verify state tracking
- TUI: add recipe with missing dep that fails OS check, verify error

### Manual Tests

- TUI: add recipe with missing dep, verify auto-add message
- CLI: `--recipes devtools` without docker, verify error
- cloudinit: create VM, wait for boot, `stoat apply`, check `v.Applied`

---

## Implementation Notes

### Order Matters

1. **Item 1** first — deletes v1, fixes broken cloudinit backend
2. **Item 2** — dependency ordering, no blockers
3. **Item 3** — cloudinit markers, depends on item 1's v2 conversion
4. **Item 4** — dry-run, independent
5. **Item 5** — docs only
6. **Item 6** — validation only, can land anytime

### Backwards Compatibility

- v1 flat-file recipes are swept to `.v1-removed/` with a warning; users must migrate
- Existing v2 recipes without `depends` keep working (empty deps = no ordering constraint)
- Existing cloudinit VMs get their `v.Applied` populated on first post-boot `Apply()`
- `stage = "install"` recipes error at add-time until the feature is built
