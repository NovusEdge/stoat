# Core API: Operation Surface

**Status:** proposal for review. Branch `core-api`. Written 2026-08-02.

**Companion to:** [`guest-subsystem.md`](guest-subsystem.md) (§9 defines why this layer exists and where it sits). This document defines *what it does*.

**Consumers:** the TUI, the CLI, and an MCP server. Every operation must be callable with no interactive input and must return structured data, because two of those three cannot answer a prompt or read a rendered string.

**Design rule used throughout:** an operation earns its place if a caller would otherwise have to reimplement it by combining others *and could get it wrong*. `Restart` is just `Stop`+`Start` and does not earn a slot. `Clone` looks like `Create`+copy but is not: getting it right means qcow2 backing files, a fresh SSH port, a new host key and a new cloud-init instance ID.

---

## 1. Types

```go
type State string

const (
    StateStopped      State = "stopped"
    StateStarting     State = "starting"      // qemu up, guest not reachable yet
    StateRunning      State = "running"       // reachable
    StateApplying     State = "applying"      // recipes executing
    StateFailed       State = "failed"        // last operation failed; see Error
    StateBroken       State = "broken"        // vm.toml unreadable
)

type VM struct {
    Name     string
    OS       string
    Mode     string        // live | disk | cloud
    Backend  string        // apkovl | cloudinit | ssh
    State    State
    RAM      int
    CPUs     int
    Disk     string
    Share    string
    Recipes  []string
    SSHPort  int
    SSHUser  string
    Installed bool
    Paths    Paths
    Progress *Progress     // non-nil only while a staged operation runs
    Error    string        // populated in StateFailed
    Created  time.Time
}

type Paths struct {
    Dir, Disk, ConsoleLog, ApplyLog, VNCSocket, MonitorSocket string
}

type Progress struct {
    Phase   string    // boot | cloud-init | apply
    Stage   string    // current named stage
    Stages  []string  // declared up front; empty when genuinely unknowable
    Done    int
    Started time.Time
}
```

`Stages` being declared up front is what makes a progress bar honest. When it is empty the caller must render elapsed time and a spinner, never a percentage: the existing rule in `provstep.go` generalised.

---

## 2. CRUD

| Operation | Signature | Notes |
|---|---|---|
| **Create** | `Create(ctx, Spec) (VM, error)` | Declarative, one call. Allocates port, writes `vm.toml`, creates disk. Does **not** start unless `Spec.Start`. |
| **List** | `List(ctx) ([]VM, error)` | Includes broken VMs as `StateBroken` rather than omitting them; today they are a separate concept the caller must know about. |
| **Get** | `Get(ctx, name) (VM, error)` | Full record including live state and progress. |
| **Update** | `Update(ctx, name, Patch) (VM, error)` | Only fields safe to change; see §2.1. |
| **Destroy** | `Destroy(ctx, name, DestroyOpts) error` | Stops first if running. `KeepDisk` for the "unregister but keep data" case. |

```go
type Spec struct {
    Name      string   // empty -> generated word pair (brisk-otter); see §8
    Image     string   // catalog ID, or an absolute path for BYO
    Mode      string   // empty -> the image's natural mode
    RAM       int      // 0 -> default
    CPUs      int
    Disk      string
    Share     string
    Recipes   []string
    ConsolePassword string // empty -> default; "random" -> generated

    Start     bool  // start after creating
    Apply     bool  // run recipes once reachable (no prompt; see §6)
    Wait      bool  // block until running/applied rather than returning early
}
```

### 2.1 What `Update` may change

Changing some fields on an existing VM is meaningless or destructive, and the current edit screen already encodes these rules (`edit.go`'s `validate`). The API must not be looser than the TUI.

- **Safe, applies at next start:** RAM, CPUs, Share, SSH port.
- **Safe, applies immediately:** recipe list (it only affects the next apply).
- **Constrained:** disk size may only grow, and only in absolute terms: `qemu-img resize` reads a leading `+` as *grow by*, which is how an 8G disk silently became 16G (fixed on the create path; the same rule applies here).
- **Not changeable:** name (that is `Clone`+`Destroy`), OS, backend, mode after first boot.

`Update` returns `ErrImmutableField` naming the field rather than silently ignoring it.

---

## 3. Lifecycle

| Operation | Signature | Notes |
|---|---|---|
| **Start** | `Start(ctx, name, StartOpts) error` | `Wait` blocks until reachable. |
| **Stop** | `Stop(ctx, name, StopOpts) error` | Graceful via monitor `system_powerdown`, `Force` for immediate kill, `Timeout` before escalating. |
| **Wait** | `Wait(ctx, name, Until) error` | Block until `Reachable` / `Applied` / `Stopped`. Cancellable. |

No `Restart`: `Stop` then `Start`, and a caller cannot get that wrong.

`Wait` is separate from `StartOpts.Wait` because a caller that started a VM earlier (or found one already starting) still needs to block on it. An agent polling `Get` in a loop is the failure mode this prevents.

---

## 4. Applying recipes

| Operation | Signature | Notes |
|---|---|---|
| **Apply** | `Apply(ctx, name, ApplyOpts) error` | Runs the VM's recipes. `Only []string` to run a subset. Streams progress into `VM.Progress`. |
| **Recipes** | `Recipes(ctx, RecipeFilter) ([]Recipe, error)` | What applies to a given OS/backend, **with its declared metadata**: name, description, stages, requirements. |
| **CheckRecipes** | `CheckRecipes(ctx, os, backend, names) ([]RecipeIssue, error)` | Validates applicability *before* creating. Returns why each is unusable rather than a bare bool. |

`CheckRecipes` is what makes the enforcement decision usable by an agent: it can ask "will these work on Alpine?" and get `xfce: requires systemd, alpine uses openrc` instead of building a broken VM and reading a black screen.

---

## 5. Access

| Operation | Signature | Notes |
|---|---|---|
| **Exec** | `Exec(ctx, name, cmd []string, ExecOpts) (ExecResult, error)` | Structured `{Stdout, Stderr, ExitCode}`. The single most valuable operation for an agent. |
| **SSHCommand** | `SSHCommand(ctx, name) ([]string, error)` | The argv a human would run, for the TUI's "drop me into a shell" and for printing. Does not execute. |
| **CopyTo / CopyFrom** | `CopyTo(ctx, name, local, remote) error` | Files in and out without making the user set up a share. |
| **Logs** | `Logs(ctx, name, Which) (io.ReadCloser, error)` | `Console` or `Apply`. Streaming, so a TUI can tail. |

`Exec` does not gate at this layer (§8, decision 1): it is a library call, and the TUI and CLI already let a user run anything. Enforcement lives in the MCP server, which is the boundary an agent crosses.

---

## 6. Consent, replacing the prompt

Applying recipes currently *asks* "run recipes? y/N", for a stated and good reason: running a script inside a guest unasked is indistinguishable from a bug.

The API keeps the principle and moves the decision to the caller:

- `Spec.Apply`: intent stated at create time.
- `Apply(...)`: an explicit call.
- Never implicit. `Start` alone never applies recipes.

The TUI keeps its prompt and passes the answer. The MCP server requires the agent to have asked for it. Neither can apply recipes by accident.

---

## 7. Convenience operations

These are the "quick VM-based testing" features that make stoat pleasant to use. All are cheap because of what QEMU and qcow2 already give us.

| Operation | Signature | Why it earns a slot |
|---|---|---|
| **Clone** | `Clone(ctx, name, newName) (VM, error)` | A qcow2 overlay backed by the source disk is near-instant and tiny. But it is *not* a file copy: the clone needs a fresh SSH port, a new host key, a new cloud-init instance ID (or cloud-init will treat it as the same instance and skip first-boot), and a new MAC. Getting that wrong produces two VMs that fight. **Highest-value convenience item.** |
| **Snapshot** | `Snapshot(ctx, name, label) error` | `qemu-img snapshot -c` on a stopped VM; QMP `savevm` for live. "Set it up, snapshot, break it, restore" is the core testing loop, and it is the single feature that makes stoat *better* than re-creating a VM rather than merely faster. |
| **Restore** | `Restore(ctx, name, label) error` | Reset to a known state without a rebuild. For an agent, this is how you get a clean environment per task without paying a full create. |
| **Snapshots** | `Snapshots(ctx, name) ([]Snapshot, error)` | List with labels, sizes, timestamps. |
| **Forward** | `Forward(ctx, name, []PortForward) error` | Today only :22 is forwarded. Testing a web service in a VM means reaching :8080, currently impossible without hand-editing QEMU args. Applies at next start. |
| **Images** | `Images(ctx) ([]Image, error)` | Catalog plus local, with download state and size. |
| **DownloadImage** | `DownloadImage(ctx, id, progress chan<- Progress) error` | Cancellable: today `esc` leaves the goroutine running, a known open item. |
| **Doctor** | `Doctor(ctx) ([]Check, error)` | Structured dependency/environment checks, not printed text. |
| **Prune** | `Prune(ctx, PruneOpts) ([]string, error)` | Remove broken VMs and orphaned images. |

### 7.1 Build order, cheapest first

All of these are wanted. Ordered by effort, so the cheap wins land early and the expensive ones are informed by real use.

| # | Item | Effort | Why here |
|---|---|---|---|
| 1 | `SSHCommand` | trivial | The argv already exists; it just needs returning instead of executing. Also fixes the `ssh.go:49` hardcoded-`root@` bug on the way. |
| 2 | `Logs` | trivial | Both files already exist on disk (`console.log`, the apply log). This is an opener plus a `Which`. |
| 3 | `Doctor` | low | `installer/checks.go` already does the checks; this returns them as data instead of printing. |
| 4 | `Images` / `DownloadImage` | low | `iso.Catalog`/`Download` exist. New work is download *state* and making cancellation real (today `esc` orphans the goroutine). |
| 5 | `Forward` | low | A `[]PortForward` on the VM, rendered into the existing `hostfwd` argument. Config field plus arg construction. |
| 6 | `Exec` | medium | ssh exec with structured `{stdout, stderr, exit}`. Mechanically simple; the *safety* design is the work (§8.1). |
| 7 | `CopyTo` / `CopyFrom` | medium | scp against the same connection settings. Care needed around paths and the guest user. |
| 8 | `Prune` | medium | Needs a confident definition of "orphaned" for images and broken VMs: deleting the wrong thing is unrecoverable. |
| 9 | `Clone` | high | Not a copy: backing-file overlay, fresh SSH port, new host key, new MAC, **new cloud-init instance ID** or first boot is skipped. Every one of those is a silent failure if missed. |
| 10 | `Snapshot` / `Restore` / `Snapshots` | high | Two mechanisms (§8.2), and it forces the monitor→QMP migration. Highest value of the lot, and worth doing properly rather than early. |

**Deliberately not included:**

- `Restart`: trivially `Stop`+`Start`.
- `Rename`: `Clone`+`Destroy`, and an in-place rename would have to rewrite paths, the cloud-init instance ID and the host key anyway.
- Template/golden-image export: `Clone` plus `Snapshot` covers the need; revisit if it doesn't.
- Live migration, resource hot-plug, nested virtualisation: not what stoat is for.

---

## 8. Decisions, settled 2026-08-02

**1. `Exec` safety: allow freely at the API layer; enforce at the MCP layer.** `core.Exec` does not gate: it is a library call, and the TUI and CLI already let a user run anything. The MCP server is where enforcement lives, because that is the boundary an agent crosses. Design pending research into what MCP actually guarantees versus what clients choose to honour (§8.1). Intent: deterministic blocking of host-reaching and destructive operations, plus human approval for the rest, rather than trusting an agent to behave.

**2. Snapshot mechanism: both, chosen by VM state.** Stopped → `qemu-img snapshot -c/-a` (simple, no running process needed). Running → QMP `savevm`/`loadvm` (captures RAM, no stop required). One API, two implementations, picked by state. Note this makes QMP a hard requirement: stoat currently speaks the *human* monitor protocol, and `sendkey.go:11-13` already flags the eventual switch to QMP as "one edit", this is what forces it.

**3. `Create` requires the image, with an auto mode.** Default `Spec.AutoDownload = true` for convenience; when false, a missing image returns `ErrImageNotDownloaded` and the caller fetches it explicitly with progress. Explicit is what an agent wants; auto is what a human wants; the flag defaults to the human.

**4. Generated names are random word pairs** (`brisk-otter`, `tidal-ember`), not sequential (`alpine-1`). Sequential names collide with a destroyed-and-recreated VM and read as an ordering that means nothing. A name may always be given explicitly.

**5. Mutability depends on VM state, and changes are classed.** Not all changes are equal:

| Class | Stopped | Running |
|---|---|---|
| **Cosmetic**: recipe list, share path | allowed | allowed (affects next apply/boot) |
| **Boot-time**: RAM, CPUs, SSH port, port forwards | allowed | allowed, takes effect at next start, and the API says so |
| **Destructive**: disk grow | allowed | **refused** while running |
| **Immutable**: name, OS, backend, mode after first boot | refused | refused |

`Update` returns `ErrRequiresStopped` (naming the field) rather than silently deferring a change the caller believes took effect. The distinction that matters: "applied later" and "refused" must never look the same to a caller.

### 8.1 Still open

- **The MCP enforcement design.** What the protocol guarantees, what clients honour, and which operations get deterministic blocks versus human approval. Research in flight.
- **What counts as "host-reaching".** A VM with a 9p share can write host files; that is the share's purpose. Whether `Exec` on a shared VM is treated as more dangerous than on an unshared one is a real question.

---

## 9. Error taxonomy

Typed errors, because every caller branches on them and string matching is how that goes wrong:

```
ErrNotFound            ErrNameTaken           ErrImmutableField
ErrImageNotDownloaded  ErrRecipeNotApplicable ErrNotRunning
ErrAlreadyRunning      ErrTimeout             ErrDependencyMissing
ErrBroken              ErrDiskShrink
```

Each carries the specific subject (which field, which recipe, which dependency) rather than only a message.

---

## 10. Security model

### 10.1 The principle

**Enforcement lives in code; approval is defence in depth.** This is forced by what MCP actually guarantees, not by preference.

Verified against the spec (`2025-06-18`):

- Human-in-the-loop is **`SHOULD`**, not `MUST`: *"there SHOULD always be a human in the loop with the ability to deny tool invocations."* A compliant client may never prompt.
- Tool annotations (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`) are **advisory**. The MCP project's own blog: *"Hints inform decisions; contracts enforce them"* and annotations *"are not guaranteed to faithfully describe tool behavior."* The single normative statement about them is a client obligation to **distrust** them, not to honour them.
- **Elicitation** (a server asking the user something mid-call) is a *negotiated capability*. A client that does not declare it leaves the server with no protocol-defined way to confirm anything, and the spec defines no fallback.
- Claude Code has a `bypassPermissions` mode whose own documentation says to use it *"in isolated environments like containers or VMs where Claude Code can't cause damage."*

But the spec **is** normative in the other direction: *"Servers **MUST**: validate all tool inputs, implement proper access controls, rate limit tool invocations, sanitize tool outputs."*

So: a guarantee stoat wants must be enforced by stoat. Client-side approval is welcome and should be supported well, but it is never the boundary.

### 10.2 The 9p share: read-only host, writable sandbox

Today there is one export: the user's chosen directory, read-write, `security_model=none`, `mount_tag=host` (`args.go:75`, fstab written by `apkovl.go:134`). An agent with `Exec` on such a VM has a direct host write path.

Proposed model, two exports:

| Tag | Host path | Access | Security model |
|---|---|---|---|
| `host` | the user's chosen directory | **`readonly=on`** | `mapped-xattr` |
| `work` | `~/.stoat/shared/<vm>/` | read-write | `mapped-xattr` |

**Why `readonly=on` works:** it is enforced by QEMU on the host side, *"Enables exporting 9p share as a readonly mount for guests. By default read-write access is given."* The guest cannot remount it writable; the export itself refuses writes.

**Why path-traversal cannot be blocked in stoat, and what replaces it:** stoat never sees 9p operations, QEMU serves them directly. A guest can create a symlink inside the share pointing anywhere, and under `security_model=none` that is a *real symlink on the host*, so any host-side path check stoat could write is defeated before it runs.

The mechanism that does work is `security_model=mapped-xattr`, which stores *"uid, gid, mode bits and link target as file attributes"*. A guest-created symlink stops being a host symlink, so it cannot be followed out of the export. This is the actual traversal defence; it is QEMU's, not ours.

**Costs, stated honestly:**

- `mapped-xattr` needs host xattr support. Fine on ext4/btrfs; must be detected, not assumed, and degrade with a clear message rather than silently falling back to `none`.
- Files the guest creates appear on the host owned by the QEMU user with `fmode`/`dmode` permissions rather than natural ownership. For an exchange directory that is a visible behaviour change.
- **This is a breaking change for existing VMs.** A share that is writable today becomes read-only. Anyone whose workflow writes to `/mnt/host` from inside a guest breaks. Needs either a per-VM opt-out (`ShareWritable bool`, default false for new VMs, true for existing ones on migration) or a stated one-time break. **Decision needed.**

### 10.3 MCP tool taxonomy

Three classes, annotated honestly and enforced independently of whether the client honours the annotation.

| Class | Tools | Annotations | Server-side rule |
|---|---|---|---|
| **Read-only** | `list_vms`, `vm_status`, `list_images`, `list_recipes`, `check_recipes`, `logs`, `doctor` | `readOnlyHint: true`, `destructiveHint: false` | Always allowed. Cannot mutate by construction. |
| **Mutating** | `create`, `start`, `stop`, `apply_recipes`, `update`, `clone`, `snapshot`, `restore`, `forward` | `readOnlyHint: false`, `destructiveHint: false` (additive) | Allowed. Bounded: they can only affect stoat-managed VMs. |
| **Destructive** | `destroy`, `prune` | `destructiveHint: true` | Allowed but irreversible; the natural candidate for elicitation when the client supports it. |
| **Execution** | `exec`, `copy_to` | `destructiveHint: true`, `openWorldHint: true` | Runs arbitrary code in a guest. See below. |

**What the server blocks deterministically**, regardless of client behaviour:

1. **No host paths as arguments.** `exec` runs *in a guest*; it never accepts a host path. `copy_to`/`copy_from` accept host paths only under `~/.stoat/shared/<vm>/`, resolved and checked after `filepath.EvalSymlinks`, rejecting anything outside; this check is possible here precisely because stoat performs the copy itself, unlike 9p.
2. **VM name must resolve to a stoat-managed VM.** No arbitrary target.
3. **Rate limiting**, which the spec makes a server `MUST`.
4. **`additionalProperties: false`** on every tool schema, so unexpected parameters are rejected rather than ignored (OWASP MCP guidance).
5. **Tool descriptions carry no hidden instructions** and are the full text a user would see: the defence against description-poisoning being invisible in a client's abbreviated UI.

**Optional, opt-in:** `Spec.AllowExec` recorded per VM, so `exec` can be refused on VMs never created for it. Default: allowed, since the VM is the containment boundary and refusing by default makes stoat useless to an agent. The share model in §10.2 is what makes that default defensible: an agent's `Exec` has no host write path unless one was explicitly granted.

### 10.4 Threat model: what this does and does not defend

**Defends against:**

- An agent (or an injected instruction reaching one) writing host files through a guest: the read-only share plus `mapped-xattr` removes the path.
- Symlink escape from the share: `mapped-xattr`.
- A client that never prompts, or runs in a bypass mode: server-side rules do not depend on prompting.
- Unexpected tool parameters: strict schemas.

**Does not defend against, and must not be claimed to:**

- **A malicious guest escaping QEMU.** That is QEMU's boundary, not stoat's. If it falls, everything above is irrelevant.
- **An agent destroying VM state it was legitimately given access to.** `destroy` on the wrong VM is a correctly-executed instruction; snapshots are the mitigation, not permissions.
- **Prompt injection in general.** The documented "lethal trifecta" (a tool that acts, untrusted input, and an exfiltration channel) is live here: an agent that runs a command in a guest, reads its output, and acts on it can be steered by that output. Narrowing the share limits the *blast radius*; it does not stop the agent being fooled.
- **A poisoned or rug-pulled tool definition** from *another* MCP server in the same session influencing how stoat's tools get called. Cross-server interference is the host's problem.

---

## 11. Concurrency

Once more than one caller exists, two current mechanisms are unsafe:

- **`config.FreePort()`** reads every `vm.toml` to find claimed ports, then returns the first free one. Two concurrent `Create` calls get the **same port**.
- **VM directory creation and `vm.toml` writes** are unlocked, so two `Create` calls with the same name race.

The API needs a data-root lock held across allocate-and-write in `Create`, and per-VM locks for state transitions. Invisible until it corrupts something, so it is designed in rather than added after.

## 12. CLI conventions

Every new command follows these. A reviewer rejects a PR that does not.

- A command's `--json` data is a struct in `internal/cli/wire`, never an
  inline `map[string]any`. The struct is the schema; `json.md` documents
  it; the MCP server reuses it.
- `a.fail(stdout, stderr, err)` for an error that came from `core`.
  `a.failMsg(stdout, stderr, sentinel, msg)` for a condition the CLI
  detected itself, such as a missing flag or a refused prompt.
- A destructive command calls the shared `confirm` helper: `-y` skips
  the prompt, `--json` and `--quiet` refuse without `-y`, a TTY prompts.
  No command hand-rolls that branch.
- A command that aliases another declares `aliases:"..."` on the kong
  struct and shares one `toArgs` case. No duplicate command struct.
- Every TOML file type decodes through `internal/tomlx`, which wraps the
  path into errors and reports unknown keys.
- Lists in a `wire` struct are never `null`; use `nonNil`.
