# Guest Subsystem: Design

**Implementation status:** the guest registry uses bundled TOML files in
`internal/guest/bundled/` with overrides from `~/.stoat/guests/`, loaded
through `internal/tomlx`. See the [guest reference](../reference/guest.md)
for the current file format. The problem inventory and proposed types below
describe the original design context.

**Status:** accepted design, written 2026-08-02. The original operation
surface is described in [Core API](core-api.md).

**Why this exists:** guest-OS knowledge is scattered across 25 sites as ad-hoc string comparisons, provisioning has no contract at all, and the logic that creates a VM lives inside a Bubbletea form. Adding Alpine cloud support missed three of those sites and the feature silently did not work. This document defines the subsystem that makes that class of failure structural rather than a matter of remembering, and the API layer that lets something other than a keyboard drive stoat.

**Scope note:** this is a rewrite of how stoat models guests and provisioning, not a patch over the existing shape. Where the current structure is wrong it gets replaced. The affected surface is ~1,000 lines of production code (`qemu` 450, `cloudinit` 231, `apkovl` 159, `recipes` 131), plus the orchestration currently embedded in `internal/tui/form.go`.

**Product context that drives §9.** stoat exists because VirtualBox is heavy and hand-downloading ISOs is tedious: the point is a quick, convenient local VM with a TUI for people who want one. The stated direction is to also expose stoat over an **MCP server so agents can drive it**. That is not a later bolt-on: it decides where the orchestration logic has to live, and today it lives in the wrong place.

---

## 1. The problem, stated precisely

Three separate failures with one cause.

**a. OS facts have no home.** 25 sites decide something based on the guest OS (full inventory below). Nothing enumerates them. Nothing fails when one is missed. Adding a distro means finding all of them from memory.

*Evidence:* Alpine cloud support added `guestShell` and `extraPackages` but missed `matchLocalImage` (entry unselectable), `iso.Infer` (empty OS on a BYO Alpine image, which re-creates the fatal bash bug) and `recipes.cloudOS` (zero recipes offered, still open).

**b. Backends are code paths pretending to be a field.** `apkovl` / `cloudinit` / `ssh` differ in which package builds a pre-boot artifact, which QEMU arguments get added, what "ready" means, and how recipes are delivered. Today that is expressed as `if v.OS == "alpine"` in `qemu/args.go:101` and `qemu/run.go:126`, plus a `switch v.Mode` elsewhere. `internal/apkovl` is Alpine's overlay format hardcoded as though nothing else could ever boot live, with every caller gating entry on a string comparison the package itself never checks.

**c. Recipes have no contract.** A recipe is a bare file whose name encodes an OS. It receives no environment, declares no requirements, reports no progress, and has no stated failure or re-run semantics. Cloud fragments are worse: `mergeCloudRecipes` understands only `packages:` and `runcmd:` and **silently discards every other key**, so a fragment using `write_files:` vanishes without a warning.

*Evidence:* `xfce.arch.sh` hardcodes `/mnt/host` because it cannot ask where the share is. A stale `xfce.cloud.yaml` installed `xfce4` without `xorg-server`, autologged root into a failing `startx`, and respawned `getty@tty1` until systemd hit the start limit: a black console with a working serial line, and nothing in the system able to say "this recipe cannot work here".

---

## 2. What prior art settles

Researched: Dev Container Features, Vagrant shell provisioner, Packer shell provisioner, Ansible roles, cloud-init's own merge and schema machinery.

| Question | What everyone does | What stoat will do |
|---|---|---|
| How does a script learn its context? | Environment variables, universally. Packer injects `PACKER_BUILD_NAME`, `PACKER_BUILDER_TYPE`, `PACKER_HTTP_ADDR`. devcontainer injects `_REMOTE_USER`, `_REMOTE_USER_HOME`, and exports every declared option at its default so nothing is ever unset. | Same. A defined `STOAT_*` environment, always fully populated. |
| How is compatibility declared? | **Nowhere enforced.** devcontainer has no field and says so explicitly. Ansible's `platforms:` is Galaxy metadata with no runtime check. Vagrant and Packer have nothing. All discover incompatibility by failing. | **Enforce it.** See §5, this is a deliberate departure. |
| Failure semantics | Only Packer documents them: non-zero fails, `valid_exit_codes` overrides, `max_retries` retries. | Non-zero fails and aborts the rest (current behaviour). Written down. No retry machinery until something needs it. |
| Idempotency | Required by no one. Ansible treats it as a module design principle, explicitly not a guarantee for shell tasks. | State plainly that recipes may re-run and handling that is the recipe's job. Do not imply a guarantee stoat does not provide. |
| Re-provisioning on later boots | Vagrant does not re-provision on resume without an explicit `--provision`. | Auto-run only on the first boot after create; keep the prompt thereafter. |
| Ordering between units | devcontainer distinguishes `dependsOn` (hard, recursive) from `installsAfter` (soft, orders only what is already selected), resolved by a round-based sort that errors on an unsatisfiable graph. | Not needed yet: recipes run in selection order. The metadata format must leave room for it (§5) so adding it later is not a format change. |
| Merging config fragments | cloud-init has a full merge system (`merge_how`, mergers `list`/`dict`/`str` with `append`/`prepend`/`no_replace`/`replace`/`recurse_*`) and a **cloud-config-archive** format that accepts a list of `{type, content}` documents and merges them itself. Default is `no_replace=True`, duplicate keys do **not** append. | Stop hand-rolling. §6. |
| Validating config | `cloud-init schema -c FILE --annotate` validates **offline, before boot**, and rejects unknown keys outright: *"Additional properties are not allowed"*. | Use it when present on the host. §6. |
| Progress reporting | `cloud-init status --format json` exposes per-stage records (`init-local`, `init`, `modules-config`, `modules-final`) each with `errors`/`start`/`finished`, plus `recoverable_errors` and `boot_status_code`. Exit code 2 means recoverable error (23.4+). | Replace text-line parsing. §7. |

**The one place stoat should exceed prior art:** compatibility enforcement. The general tools cannot enforce it because they target arbitrary machines: Vagrant provisions any box, Packer targets named build sources. stoat *records the guest OS in `vm.toml` at create time*, so it can refuse an inapplicable recipe in the create form, before a VM boots. That is the check that would have turned the black screen into a one-line message.

---

## 3. The architecture

Two concepts, deliberately separate: **what an OS is** (data) and **how a guest is provisioned** (behaviour). Conflating them is what produced the current mess, where `v.OS == "alpine"` is used to mean "uses an apkovl".

### 3.1 `guest.OS`: data

One declaration per distro. Every field exists because a call site consumes it today (§4 maps them).

```go
// Package guest owns everything stoat knows about guest operating systems and
// how they are provisioned. It imports only internal/config, so every package
// that needs it can.
package guest

type OS struct {
    Name string // canonical identity, matches vm.toml's os field

    // Shell is the login shell for a seeded account. It MUST exist in the
    // image: cloud-init's user module fails outright on a missing shell,
    // leaving no account and no authorized_keys, and the only symptom is
    // "Permission denied (publickey)" forever.
    Shell string

    // SeedPackages are packages the base seed ASSUMES are present but which
    // this image does not ship. Alpine needs sudo: the users: sudo key writes
    // a sudoers fragment, and the binary is absent (the cloud-init aport
    // prefers doas).
    SeedPackages []string

    // Backend names how this OS is provisioned. It is an OS fact, not a mode.
    Backend BackendName

    // Installer is the interactive install command named in UI hints.
    // Empty means "the installer" generically.
    Installer string

    // DefaultSSHUser is who to connect as when nothing overrides it.
    DefaultSSHUser string

    // FilenameHints recognise this OS in a BYO image filename. An empty OS on
    // a BYO image is the dangerous case: it silently selects every default,
    // which is how an Alpine image gets asked for /bin/bash.
    FilenameHints []string
}

func Lookup(name string) (OS, bool)
func All() []OS
```

### 3.2 `Backend`: behaviour

The part a struct of fields cannot express. Three implementations, one interface.

**This interface does not live in package `guest`.** `guest` must stay a
zero-import leaf so every package that needs OS facts can depend on it
without risk, and `internal/cloudinit`, `internal/recipes` and
`internal/iso` already import `guest` today. An implementation needs
`apkovl`, `cloudinit`, `recipes` and `keys`; putting `Backend` in `guest`
would make `guest` import those back, closing a cycle. It lives in a new
package, `internal/backend`, which imports `guest` (for `OS.Backend`) plus
everything an implementation needs; `internal/qemu` imports `internal/backend`
in turn. The type sketch below is otherwise unchanged from the original
design.

```go
type BackendName string

const (
    BackendAPKOVL    BackendName = "apkovl"
    BackendCloudInit BackendName = "cloudinit"
    BackendSSH       BackendName = "ssh"
)

type Backend interface {
    Name() BackendName

    // Prepare builds whatever pre-boot artifact this backend needs, before
    // QEMU starts: an apkovl tarball, a NoCloud seed ISO, or nothing.
    // Called on every start for apkovl (the overlay is rebuilt) and once for
    // cloudinit (the seed is authoritative for the instance's life), that
    // difference is the backend's own business, not the caller's.
    Prepare(v *config.VM, r []Recipe) error

    // Args contributes this backend's QEMU arguments: the vvfat overlay
    // drive, the seed cdrom, or nothing. Pure, no filesystem access.
    Args(v *config.VM) []string

    // Ready reports when the guest is usable. SSH-reachable for most; for
    // cloudinit it additionally means cloud-init finished.
    Ready(ctx context.Context, v *config.VM) error

    // Provision runs the post-boot recipes, if this backend has any. For
    // cloudinit this is a no-op: its recipes ran at first boot from the seed.
    Provision(v *config.VM, r []Recipe) error
}

func BackendFor(os OS) Backend
```

What this buys, concretely:

- `qemu.Args` stops asking "is this Alpine?" and asks the backend for its arguments. The two `v.OS == "alpine"` comparisons (`args.go:101`, `run.go:126`) disappear.
- `internal/apkovl` stops being a package every caller string-gates into. It becomes the apkovl backend's `Prepare`, and the Alpine assumption lives inside the thing that is allowed to assume it.
- Adding a distro is one `OS` declaration. Adding a *provisioning mechanism* is one `Backend` implementation. Neither requires finding call sites.

### 3.3 What stays out

`guest` owns OS identity and provisioning. It does **not** own:

- **Image catalog entries** (`iso.Entry`): those are per-image (URL, checksum, size, flavor), many rows per OS. `iso` consumes `guest` for filename inference; the catalog itself stays put.
- **Mode** (`live`/`disk`/`cloud`): a per-VM choice, not an OS fact. Backend and Mode are related but distinct: Alpine's backend is apkovl in both live and disk mode.
- **Download/checksum format handling**: GNU vs BSD sums correlate with a distro's mirror infrastructure, not the OS. Stays in `iso`.

---

## 4. Call-site inventory and disposition

From a full read of the tree. Every site that branches on guest OS today.

| # | Site | Today | After |
|---|---|---|---|
| 1 | `iso.Infer` (iso.go:307) | filename → (backend, os); only matches "alpine" | `guest.All()` + `FilenameHints` |
| 2 | `cloudinit.guestShell` (:57) | `=="alpine"` → ash else bash | `OS.Shell` |
| 3 | `cloudinit.extraPackages` (:76) | `=="alpine"` → sudo | `OS.SeedPackages` |
| 4 | `qemu/args.go:101` | `=="alpine"` → attach vvfat | `Backend.Args` |
| 5 | `qemu/run.go:126` | `=="alpine"` → build apkovl | `Backend.Prepare` |
| 6 | `recipes.cloudOS` (:75) | `{ubuntu,debian,arch}` set | recipe metadata (§5) + `OS` |
| 7 | `recipes.List` (:88) | filename-suffix parsing | recipe metadata (§5) |
| 8 | `scaffold.osSetup` (:61) | pkg manager per OS | `OS` (add a `PkgInstall` field) |
| 9 | `provision.installerName` (:16) | `=="alpine"` → setup-alpine | `OS.Installer` |
| 10 | `form.resolvedSSHUser` (:432) | backend-conditional | `OS.DefaultSSHUser` + backend |
| 11 | `form.matchLocalImage` (:190) | gated on `Flavor` | unchanged (image concern, not OS) |
| 12 | `form.newForm` (:496) | defaults to first alpine entry | explicit default in catalog, not order-dependent |
| 13 | `tui/ssh.go:49` | **hardcodes `root@`, ignores SSHUser** | **bug**: one shared resolver |
| 14 | `apkovl` package | Alpine-only, ungated internally | the apkovl `Backend` |
| 15-25 | display/labels, edit-path backend derivation, CLI `--os` validation | scattered | read from `guest` |

**Two live bugs this surfaces**, both to fix as part of the work:

- `tui/ssh.go:49` hardcodes `root@127.0.0.1` and never reads `v.SSHUser`. On a cloud VM the account is `stoat` and root is locked, so pressing `s` **always fails**. Three other sites resolve this correctly: there are two independent implementations, one wrong. The failure text also hardcodes "setup-alpine" regardless of OS.
- `recipes.cloudOS` still lacks Alpine, so an Alpine cloud VM is offered **zero** recipes. `recipes_test.go:128-132` pins the now-wrong behaviour.

**Import direction:** `config`, `iso`, `cloudinit`, `recipes`, `apkovl` are leaves importing only `config` (plus `keys` in two cases); `qemu` sits above them; `tui` above everything. `guest` importing only `config` sits below all of them, so every consumer can import it with no cycle. Verified against the current import graph.

---

## 5. The recipe contract

Two surfaces, as distinct as their environments force them to be.

### Phase 1: pre-boot, strict

Runs before ssh exists, from the seed or overlay. No shell, no network, no package manager yet. The recipe fills declared slots; it cannot improvise because there is nothing to improvise with. Mechanically this is a cloud-init fragment (cloudinit backend) or files injected into the overlay (apkovl backend).

### Phase 2: post-boot, loose

Runs over ssh on a booted system. A shell script, with a contract:

**Metadata as in-band front-matter.** Comments in the script itself, so there is no second file to drift out of sync: the failure mode of every sidecar-metadata design, and the one that just bit us with stale recipe copies.

```sh
#!/bin/sh
# stoat:name        xfce
# stoat:description XFCE desktop with a graphical login
# stoat:os          alpine, ubuntu, debian, arch
# stoat:requires    systemd
# stoat:stages      install, configure, enable
```

- `os`: **enforced at selection time**, not discovered by failing. This is the departure from prior art, and the whole point.
- `requires`: capability names resolved against the `OS` declaration (`systemd`, `sudo`, …). Also enforced at selection.
- `stages`: declared up front, which is what makes a *real* progress bar honest rather than an invented percentage.

**A guaranteed environment**, fully populated, defaults included (devcontainer's rule: a script must never see an unset variable):

```
STOAT_VM, STOAT_OS, STOAT_MODE, STOAT_BACKEND, STOAT_USER, STOAT_SHARE, STOAT_SSH_PORT
```

**A progress protocol the recipe drives.** The recipe emits its own stage markers, rather than stoat inferring them:

```sh
echo "=== stoat:stage install ==="
```

This is the same shape stoat already writes and parses (`sshx.go:139`, `provstep.go:66-98`); the change is that the *recipe* emits it, against a declared stage list, so progress can be `2 of 5` instead of a spinner.

**Failure and re-run semantics, stated:** non-zero exit fails the recipe and aborts the remaining ones (current behaviour, now documented). Recipes may be re-run; handling that is the recipe's responsibility: stoat provides no idempotency guarantee, matching every tool researched.

---

## 6. Delete `mergeCloudRecipes`

cloud-init accepts a **cloud-config-archive**: a YAML list of `{type, content}` documents which it merges itself. stoat should hand it the fragments verbatim and stop interpreting YAML. The silent dropping of `write_files:` disappears because nothing is parsing for known keys any more.

**The catch, which must be handled explicitly:** cloud-init's default merge is `no_replace=True`: two documents both defining `packages:` do **not** append; the first wins. Current behaviour concatenates. So the archive must carry an explicit directive:

```yaml
merge_how: 'list(append)+dict(recurse_list)'
```

Getting this wrong silently drops packages, so it needs a test that merges two fragments both declaring `packages:` and asserts both survive.

**Validation:** `cloud-init schema -c FILE --annotate` checks a fragment offline and rejects unknown keys. Gate it on the binary being present on the host (the same pattern `haveXorriso()` already uses) and surface failures at recipe-selection time rather than at boot.

---

## 7. Progress and readiness

`Backend.Ready` replaces the current split between `sshx.Wait` (TCP + `SSH-` banner, 90s) and cloud-init polling.

For the cloudinit backend, stop parsing the text `status:` line and use `cloud-init status --format json`: per-stage records for `init-local`, `init`, `modules-config` and `modules-final`, each with `errors`, `start` and `finished`, plus `recoverable_errors` and `boot_status_code`. That gives a named stage list with timestamps, a legitimate progress bar, and lets stoat say *which* stage failed. Exit code 2 (23.4+) means recoverable error and must not be treated as success or as fatal.

For phase-2 recipes, progress comes from the declared `stages` plus emitted markers (§5).

---

## 8. What this deliberately does not do

- **No dependency graph between recipes.** devcontainer's `dependsOn`/`installsAfter` solve a problem stoat does not have yet; recipes run in selection order. The front-matter format leaves room to add it without a format change.
- **No retry/timeout machinery** per recipe (Packer's `max_retries`, `start_retry_timeout`). Nothing has needed it.
- **No versioning or registry distribution** of recipes. Related but separate: stale local copies are a real bug with its own fix (a hash manifest so `stoat recipe update` can tell "stoat wrote this" from "the user edited this").
- **No generalising of `apkovl`** to other distros. It stays Alpine's, just properly encapsulated as a backend rather than a package everyone string-gates into.

---

## 9. The core API layer

### 9.1 The problem

**At the time of this proposal, creating a VM was only possible by driving a
Bubbletea form.** `internal/tui/form.go`'s `build()` (:753+) resolved the image,
inferred the OS, picked the backend, allocated an SSH port via
`config.FreePort()`, wrote `vm.toml` and created the qcow2. The CLI then had
`ls`, `up`, `down`, `ssh`, `provision`, `rm`, `recipe`, `logs`, `doctor`, and
**no `create`**.

So orchestration sat *above* the layer any programmatic caller would enter at.
An MCP server would have had to either re-implement `form.build()` (a second,
drifting copy of the rules) or drive a TUI, which is absurd. The same was true
of the CLI, which is why it had no `create` command at that time.

This is a layering defect, not a missing feature.

### 9.2 The layering

```
        TUI            CLI            MCP server
          \             |             /
           \            |            /
            ┌───────────────────────┐
            │        core API       │  create · start · stop · provision
            │                       │  status · destroy · list · images
            └───────────────────────┘
                        │
            ┌───────────────────────┐
            │         guest         │  OS facts + Backend implementations
            └───────────────────────┘
                        │
      config · iso · qemu · cloudinit · apkovl · recipes · sshx
```

`guest` (§3) is necessary but not sufficient: it fixes *how a guest is provisioned*, not *who can ask for one*. The core API is what makes stoat a library that happens to ship a TUI, rather than a TUI with some packages under it.

### 9.3 Surface

Deliberately small, and shaped so every operation is one call with no interactive state.

```go
package core

type Spec struct {
    Name     string   // empty -> generated
    Image    string   // catalog entry ID, or an absolute path for BYO
    Mode     string   // empty -> the image's natural mode
    RAM      int      // 0 -> default
    CPUs     int
    Disk     string
    Share    string
    Recipes  []string
    Provision bool    // run recipes once reachable; no prompt
}

type VM struct {
    Name, OS, Mode, Backend string
    State     State         // stopped | starting | running | provisioning | failed
    SSHPort   int
    SSHUser   string
    Paths     Paths         // disk, console log, vnc socket, monitor socket
    Progress  *Progress     // non-nil while a staged operation runs
}

type Progress struct {
    Phase   string   // "boot" | "cloud-init" | "provision"
    Stage   string   // current named stage
    Stages  []string // declared up front: what makes a real progress bar honest
    Done    int
    Started time.Time
}

func Create(ctx, Spec) (VM, error)
func Start(ctx, name) error
func Stop(ctx, name) error
func Provision(ctx, name, opts) error
func Status(ctx, name) (VM, error)
func List(ctx) ([]VM, error)
func Destroy(ctx, name) error
func Images(ctx) ([]Image, error)   // catalog + local, with download state
```

### 9.4 What the agent use-case demands beyond a TUI

These are requirements the TUI never forced, and each one is a real change:

**No interactive prompts below the UI layer.** Provisioning currently *asks* "run recipes? y/N" (`autoprov.go`). An agent cannot answer a prompt. Consent becomes a parameter (`Spec.Provision`), and the TUI supplies it from its own prompt. The existing reasoning, *"running a shell script inside a guest without being asked is helpfulness indistinguishable from a bug,"* is preserved by making the caller state intent, not by asking mid-operation.

**Structured results, not rendered strings.** `Status` returns states and stages, not a formatted line. Errors are typed (`ErrNameTaken`, `ErrImageNotDownloaded`, `ErrRecipeNotApplicable`) so a caller can branch. The `cloud-init status --format json` work in §7 feeds this directly.

**Declarative creation.** One `Create(Spec)` call, no multi-step form state. Everything the form asks for either has a default or is in the `Spec`.

**Recipe metadata now gates VM creation.** §5's `# stoat:os` / `# stoat:requires`, enforced at selection, is *optional politeness* for a human who can read a black screen and *mandatory* for an agent that cannot. `Create` returns `ErrRecipeNotApplicable` rather than producing a VM that boots broken. This is the strongest argument for the departure from prior art in §2.

**Cancellation.** Every operation takes a `context.Context`. Downloads and boot-waits are long; an agent that abandons a task must be able to stop them. Today `esc` leaves a download goroutine running (a known open item).

### 9.5 Concurrency: new, and required once a second caller exists

A TUI has one user doing one thing. An MCP server plus a TUI plus a CLI can act **at the same time**, and two current mechanisms are not safe under that:

- **`config.FreePort()`** (`config.go:217-243`) scans 2200–2300, collecting claimed ports from every `vm.toml`, then returns the first free one. Two concurrent `Create` calls read the same state and get the **same port**. Today this cannot happen because only one form runs at a time; with an API it can.
- **VM directory creation and `vm.toml` writes** have no locking, so two calls creating the same name race.

The core API needs a data-root lock: a lockfile under `$STOAT_HOME` held across the allocate-and-write section of `Create`, and per-VM locks for state transitions. This is not optional once a second caller exists, and it is invisible until it corrupts something.

### 9.6 What moves, and what does not

**Moves out of `internal/tui` into `core`:** image resolution and download state, OS/backend derivation, port allocation, `vm.toml` construction, disk creation, the provisioning decision, and the readiness/progress state machine.

**Stays in `internal/tui`:** every rendering decision, key handling, the create *form* (which becomes a `Spec` builder), prompts, toasts, and the progress *display*. The TUI keeps its own model; it stops owning the rules.

**Stays where it is:** `config` (types and paths), `iso` (catalog and downloads), `qemu` (process handling), `sshx` (ssh mechanics). These are already libraries; they just get a coherent caller.

### 9.7 MCP shape (sketch, not scope)

Once `core` exists, the MCP server is a thin mapping, which is the test of whether the layering is right:

| MCP tool | core call |
|---|---|
| `stoat_create_vm` | `Create(Spec)` |
| `stoat_list_vms` | `List` |
| `stoat_vm_status` | `Status` |
| `stoat_start` / `stoat_stop` | `Start` / `Stop` |
| `stoat_provision` | `Provision` |
| `stoat_run` | `sshx` exec, structured stdout/stderr/exit |
| `stoat_list_images` | `Images` |
| `stoat_destroy` | `Destroy` |

If any of these needs logic that is not already in `core`, the layering is wrong and that is the signal to fix it, not to add the logic to the MCP server.

`stoat_run` is the one genuinely new capability (run a command in a guest, get structured output). It is also the one with a real safety question (arbitrary command execution in a VM on the user's machine) which deserves its own decision rather than riding along here.

### 9.8 Sequencing note

`guest` (§3) and `core` (§9) are separable. `guest` can land first and is useful on its own; `core` depends on it, because `Create` needs OS/backend derivation to be a library call rather than eight string comparisons. MCP depends on `core`. Doing `core` before `guest` would mean building the API on top of the scattered logic and then rewriting its internals: possible, but wasteful.

---

## 10. Risks

- **This is a real refactor**, touching `qemu`, `cloudinit`, `apkovl`, `recipes`, `iso` and `tui`. The mitigation is that behaviour must not change except where a bug is named here: existing tests are the contract, and any test that needs updating is a signal to stop and check rather than to edit.
- **`Backend.Prepare`'s call frequency differs by backend** (apkovl every boot; cloudinit once, ever). A cloud VM whose seed is rebuilt would have its instance identity change under it. The interface hides it, which is right, but the implementations must be explicit about it and tested for it.
- **`cloud-init schema` may be absent** on the host (Arch does not install cloud-init by default). Validation must degrade to "not checked", never to "assumed valid".
- **The merge-semantics change is silent when wrong.** Packages that quietly stop being installed look like a recipe bug, not a merge bug. Needs a direct test.
