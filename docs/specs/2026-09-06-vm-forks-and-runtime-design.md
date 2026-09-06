# VM forks and runtime continuation

Status: proposed, 2026-09-06. This file specifies new behavior. It is not
an installation guide or a description of shipped commands.

The agreed model has two layers: a persistent VM and its runtime. Forks
share a recorded starting point and then change independently. Runtime
preservation is preferred when supported; a fresh boot is an explicit,
observable fallback. The interfaces and defaults below are proposed for
review. No implementation or live compatibility test accompanies this spec.

## 1. Goal and scope

Any agent that can invoke the JSON CLI or MCP can create an experiment,
checkpoint it, fork alternatives, inspect their origin, and recover the
operation after a client disconnects. A bounded worker may continue an
accepted operation after the client disconnects; client disconnect does not
mean that the Stoat process survives host termination. Stoat does not require
a particular model, agent framework, conversation format, or hosted service.

The first supported storage profile is one persistent qcow2 system disk
on the local host, with the existing writable work-share export explicitly
disabled and with no device passthrough.
Both installed-disk and provisioned cloud-image VMs are candidates. Each
guest profile must pass the identity and restart gates in section 11.
Diskless live VMs remain unsupported for state-preserving forks in the
first release. Duplicating their configuration is still a separate operation.

This design covers checkpoints, forks, runtime pause/suspend/resume,
lineage, retry behavior, and evidence about these operations. Distributed
scheduling, cross-host migration, automatic disk merging, and generic
application-aware checkpoints are outside the first release.

## 2. Two layers

| Layer | Owns | Does not own |
|---|---|---|
| VM | Stable ID, name, writable disks, configuration, host access policy, parent checkpoint, and checkpoint history | A running QEMU process or the agent's conversation |
| Runtime | Runtime ID, CPU and device state, RAM, running guest processes, execution state, and compatibility fingerprint | An independently interchangeable disk image |

A VM has at most one active or suspended runtime. A cold boot creates a
new runtime ID. Suspending and resuming the same VM retain its runtime
ID, even though the host QEMU process may change. A fork always receives
a new VM ID. A resumed fork receives a new runtime ID when its saved runtime
is prepared; a cold fork receives its runtime ID on its first `up`. In both
cases it records the source runtime ID, when one exists, as ancestry. Guest process IDs can
remain numerically equal in separate VMs.

A checkpoint is an immutable record of the VM layer at a particular point.
It can also contain the matching runtime layer. A saved runtime is usable
only with the disks and guest-visible hardware captured with it.

The Git analogy is deliberately limited:

| Git concept | Stoat concept |
|---|---|
| Commit | Immutable checkpoint |
| Working tree and branch | A VM's writable state and checkpoint history |
| Branch from a commit | Fork from an explicit checkpoint |
| Log | Checkpoint and fork ancestry |

There is no implicit `main` VM, disk merge, or automatic conflict resolution.
Keeping an attempt means retaining its VM or exporting selected results.

## 3. Checkpoint contract

Proposed CLI:

```sh
stoat checkpoint create dev --name baseline --runtime auto --json
stoat checkpoint ls dev --json
stoat checkpoint show <checkpoint-id> --json
```

Every checkpoint receives an opaque global ID. A human label is unique
within its source VM and cannot be silently reused or moved. Commands
accept the ID or `VM@label`; results always return the resolved ID.
Checkpoint ancestry uses IDs, so a deleted source VM does not make a
checkpoint ambiguous.

`--runtime` selects the capture policy:

| Value | Required behavior |
|---|---|
| `auto` (default) | Include runtime state for a compatible running or paused VM, or use the already published saved runtime for a suspended source when it remains compatible. Otherwise, if the disk mechanism supports the source's current state, capture disks and record why runtime state was excluded; otherwise return `capability_unavailable` before mutation. |
| `include` | Require matching RAM, CPU, device, and disk state. An unsupported or stopped source returns an error. |
| `exclude` | Capture persistent disks and configuration only. |

Capability selection happens before capture. Under `auto`, an unsupported
runtime permits disk capture only when the disk mechanism supports the
source's current state. If it does not, Stoat returns
`capability_unavailable` before mutation. A runtime capture that starts and
then fails does not silently change into a disk-only success. Stoat reports
the failed operation, the source's actual state, and any recovery action
required.

A checkpoint records `disk_consistency` separately from runtime inclusion.
The first release guarantees a storage-level point in time; it does not
claim that database transactions or guest write buffers were flushed.
A stopped source is recorded as `offline`; this alone does not prove a
clean guest shutdown. A suspended source can use its published saved runtime,
or be captured with `--runtime exclude` as `offline`. Suspended capture
retains references to the matching immutable pair without resuming it.
A running source's disk-only capture is recorded as
`crash_consistent`. Coordinated guest/application quiescing is a later
capability, not an inferred property of a paused CPU.

Capture may pause execution. It must not silently shut down the source.
On success, a running source runs again and an already paused source stays
paused. The result reports interruption duration. On failure, Stoat reports
the observed state and never claims that resuming succeeded without checking.

The manifest includes the source VM/runtime IDs, preceding checkpoint ID,
timestamp, normalized configuration, recipe declarations and immutable refs,
disk object IDs and checksums, consistency, runtime inclusion, exclusion
reasons, external dependencies, and the compatibility fingerprint.
Unavailable recipe commits or external artifacts are reported as unresolved;
a snapshot of installed bytes does not imply reproducible provisioning.

## 4. Immutable storage and publication

A fork must never use another VM's active writable disk as its backing
file. The parent must remain free to run and change after capture.

Each checkpoint owns an immutable disk view. A disk-only fork receives a
private writable overlay backed by that view. A saved-runtime fork may
require a complete private image copy or reflink to preserve internal
snapshot metadata. The selected mechanism must pass its own restore gate;
an overlay path cannot borrow a full-copy path's capability result.
A published disk view may depend only
on other immutable, tracked objects. Parent and sibling writes must not
change its visible bytes.

For the initial implementation, a self-contained qcow2 checkpoint is an
acceptable baseline. Stoat may use filesystem reflinks to avoid physical
copying when supported. If it must copy the full image, the plan reports the
copy and expected space requirement. Constant-time forks do not imply
constant-time checkpoint creation.

A stopped disk can be materialized with an offline image operation. A
running disk requires a coordinated QMP operation. Copying an active qcow2
file directly, or forcing image locks, is not an allowed capture mechanism.
Before any storage mutation, the implementation must reconcile the operation
journal, acquire the mandatory OS-held storage mutation lock and reservation,
and confirm QEMU ownership of every source object. Concurrent storage
operations are excluded; an active or uncertain QEMU owner requires
reconciliation before storage proceeds. The runtime capture/export mechanism
must pass the feasibility gate before the implementation advertises runtime
forking. Existing internal snapshots alone do not establish that a child can
load state through an overlay.

Publication order is: reserve the operation, capture into a temporary
directory, validate the complete disk/runtime pair, flush files and manifest,
then atomically publish the checkpoint. Only published checkpoints may be
forked. A partial checkpoint is never listed as ready.

The object graph governs deletion. Removing a VM does not remove shared
checkpoint objects. Removing a referenced checkpoint returns `in_use` with
dependent IDs. There is no force flag that breaks descendants. Pruning is
explicit and dry-run by default. It protects active operations, suspended
runtimes, and checkpoints referenced by forks. Recomputing references from
manifests must be possible after a crash.

## 5. Fork contract

```sh
stoat fork dev@baseline try-upgrade --runtime auto --json
stoat fork dev@baseline try-patch --runtime discard --json
stoat fork dev@baseline try-resume --runtime require --json
stoat lineage try-upgrade --json
```

Forking requires an existing checkpoint. This avoids combining an
unbounded capture operation with branch creation. The source checkpoint is
resolved once and retained throughout the operation.

| Runtime policy | Required behavior |
|---|---|
| `auto` (default) | During preflight, prepare a resumed fork if saved state exists and passes compatibility checks; otherwise prepare a cold-boot fork and return a fallback reason. |
| `require` | Refuse if a resumed fork cannot be prepared. Do not create a cold-boot substitute. |
| `discard` | Prepare a fresh boot from the checkpoint's persistent disks, even if saved runtime exists. |

The result contains `requested_runtime`, `effective_runtime` (`resume` or
`cold_boot`), and a nonempty `fallback_reason` whenever `auto` falls back.
It also states that a cold boot loses saved processes and volatile memory.
Fallback is a preflight choice. Once resumed-fork mutation or restore starts,
failure produces a failed or recovery-required operation; it never substitutes
a cold fork.
No VM starts automatically. A resumed fork is prepared in `suspended`
state with its new runtime ID; a cold fork is prepared in `stopped` state and
has no runtime ID until its first `up`.

The source VM and its runtime are untouched. Fork names must be unused.
The child has a new host-side identity, data directory, QMP socket, console
log, SSH port reservation, and evidence namespace. Extra port forwards,
project ownership, and writable host shares are not inherited. The child
appears as a global VM with origin-project metadata; project reconciliation
does not overwrite its experimental configuration.

The copied disk remains private application data. It can contain credentials,
SSH keys, machine IDs, database IDs, and active application sessions. Forking
does not claim to sanitize those bytes. Host-managed secret files are not
implicitly copied; required references are reported by preflight.

Cold forks receive a new guest identity through a tested guest-specific
initialization path. The checkpoint remains immutable; changes occur only
in the child's overlay. That path must not erase user files or rerun general
provisioning merely because the cloud-init instance ID changed. If a guest
profile cannot meet that contract, it is unsupported until a gate proves it.

Resumed forks preserve the saved guest-visible identity and device model,
including the NIC model, MAC address, and PCI topology. They use separate
user-mode network stacks and default to `restricted` networking: no
guest-initiated external traffic, with a new loopback SSH forward for
control. Extra forwards are omitted. A backend is supported only when its
compatibility gate proves that the host-side network change preserves that
guest-visible model.

The user must explicitly enable ordinary egress before cloned applications
can contact external services. Cold forks use the same restricted default.
Readiness checks that need the network report this restriction. Existing
external TCP sessions, leases, locks, and service-side identities are not
promised to survive or become independent merely because RAM was restored.

## 6. Runtime lifecycle

```sh
stoat runtime pause try-upgrade --json
stoat runtime resume try-upgrade --json
stoat runtime suspend try-upgrade --json
stoat runtime status try-upgrade --json
```

| Operation | State transition | Resource effect |
|---|---|---|
| Cold `up` | stopped → starting → running | Allocate a new runtime |
| `pause` | running → paused | Stop guest execution; retain QEMU and allocated memory |
| `resume` after pause | paused → running | Continue the same runtime in the same QEMU process |
| `suspend` | running/paused → suspending → suspended | Persist the paired VM/runtime state, then release QEMU and memory |
| `resume` after suspend | suspended → restoring → running | Validate compatibility and restore the saved pair; explicit resume always ends in `running` |

`pause` is not durable across host failure. `suspend` is durable only after
the saved pair is published and QEMU termination is confirmed. If saving
fails, Stoat must not terminate the source. A successful suspend records
the prior run state for evidence only; a later explicit resume always
continues in `running` state.

`up` on a suspended VM must not discard saved runtime. It returns an
instruction to use `runtime resume` or explicitly discard the saved runtime.
`stoat runtime discard <vm> --yes --json` transitions a suspended VM to
stopped, retaining its matching persistent disk and recording the loss of
volatile state. Without `--yes`, the CLI uses the existing confirmation
convention; JSON mode therefore requires `--yes`. The MCP
`runtime_discard` input is `{ "vm": "<name>", "confirm": true }` and
returns a `RuntimeDiscardResult` containing `vm`, `state`,
`discarded_runtime_id`, and `effective_runtime: "cold_boot"`. The next `up`
cold-boots and starts a new runtime ID.

The corresponding CLI result is `{"vm":"<name>","state":"stopped",
"discarded_runtime_id":"<runtime-id>","effective_runtime":"cold_boot"}`;
MCP returns the same fields as a structured tool result, or a machine-readable
state-conflict error when the VM is not suspended.

Pause and resume requests are idempotent when already in their target
state. `runtime resume` from `suspended` restores the saved pair and always
ends in `running`, regardless of the prior run state recorded as evidence.
Other invalid transitions return a typed state-conflict error.
Incompatible configuration changes are refused while suspended. A resume
must not fall back to a reboot. The caller must explicitly discard runtime
to choose a cold boot.

Recipe application is not repeated during a memory resume. A cold boot
uses the guest's existing startup behavior, with inherited recipe evidence
identified as evidence from the checkpoint. Health becomes unverified until
the relevant live checks run; restoring a green result is not a new check.

## 7. Compatibility and capability discovery

```sh
stoat capabilities dev --json
stoat fork dev@baseline try-upgrade --runtime auto --plan --json
```

Capabilities are evaluated for the source and this host. They include disk
checkpointing, runtime capture, runtime forking, pause, durable suspend,
resume, consistency modes, restricted networking, and rejection reasons.
Installing QEMU alone does not establish all of these capabilities.

The runtime fingerprint records QEMU binary/version, versioned machine
type, architecture, accelerator, CPU model and exposed features, CPU count,
RAM layout, guest-visible devices, firmware and NVRAM where present, and all
required storage and removable media identities. The initial supported
profile is same-host and same QEMU build. Broader compatibility requires
separate evidence; changing a host package must not silently invalidate a
saved runtime and then boot it cold.

Passthrough devices, writable host filesystems, unsupported writable media,
or missing dependencies cause a capability rejection before mutation.
`--plan` resolves policy and reports missing dependencies, isolation limits,
copy requirements, and resource estimates. It does not reserve names or
ports, so execution revalidates the plan under locks. The supported profile
also rejects Stoat's default writable work-share export explicitly before
mutation; a future implementation must opt into that export only through a
separate capability gate.

## 8. Agent contract, operations, and evidence

CLI and MCP call the same core operations and expose equivalent typed
semantics. CLI uses the JSON-lines envelope; MCP returns tool results or
structured tool errors. Field names, IDs, retryability, fallback reasons, and
state transitions must map explicitly between the two transports; byte-level
payload identity is not required.
New MCP tools are `capabilities`, `checkpoint_create`, `checkpoint_list`,
`checkpoint_show`, `checkpoint_remove`, `fork`, `fork_plan`, `lineage`,
`runtime_status`, `runtime_pause`, `runtime_suspend`, `runtime_resume`,
`runtime_discard`, `operation_status`, and `evidence_export`.

The first contract PRs are limited to additive MCP structured errors and
read-only capability discovery. They do not ship lifecycle mutations or the
future operation journal and status fields described below.

Lifecycle calls do not execute agent-supplied shell strings. Forking through
MCP preserves or lowers the source access level and cannot raise it. It
does not import a more permissive default when the source has `none`.
The existing access-level model is guest-operation policy, not a guarantee
that an agent with arbitrary host shell access is confined.

Every mutation accepts a caller-supplied `request_id`, scoped to the local
Stoat data root and operation kind, and returns an `operation_id`. The host
journals the canonical input and resolved IDs before dispatch, along with
phase, state changes, created resources, and terminal result. A retry looks
up that stored canonical record first; it does not resolve a VM name again.
Thus a name that points to a different runtime later still returns the
original historical record. Changed explicit arguments return
`request_conflict`.

The proposed operation-status record contains `operation_id`, `request_id`,
`resource_ids` (including VM, runtime, checkpoint, or job IDs where
applicable), `phase`, `state`, `created_at`, `updated_at`, optional
`completed_at`, and state-dependent outcome fields. `succeeded` requires
`result` and forbids `error`; `failed` requires `error` and forbids `result`.
`accepted`, `running`, and `recovering` contain neither outcome field.
`indeterminate` also contains neither outcome field and remains nonterminal;
it requires `observations` and `recovery_actions` explaining what remains
unknown and how to inspect it.
Its state is one of
`accepted`, `running`, `recovering`, `succeeded`, `failed`, or
`indeterminate`. `succeeded` and `failed` are terminal and immutable.
`indeterminate` requires reconciliation and never automatic replay. A
confirmed cancellation is terminal `failed` with error code `canceled`.
Jobs, evidence, and request-ID deduplication records have no automatic expiry
in the first release. Explicit prune retains a compact tombstone containing
the request fingerprint and terminal disposition after evidence is deleted,
so a late retry cannot create a second fork or restart guest work. A fresh
attempt uses a new request ID.

Lifecycle operations are synchronous initially. If the executing process
dies, completion is not guaranteed; the next caller reconciles the journal.
The companion jobs design introduces detached workers for guest commands.
A client timeout
does not prove that an operation failed or was cancelled. The next caller uses
`operation_status` to retrieve the result or detect interrupted work. Recovery
reconciles the journal with actual QEMU and disk state before another
conflicting mutation runs. It does not replay guest commands. A daemon and
detached worker API are not prerequisites for the first release.

The mandatory OS-held mutation locks and reservations, plus journal
reconciliation, protect storage and QEMU effects before any storage mutation.
They exclude concurrent storage operations and cannot be bypassed by elapsed
time: expiry never overrides a kernel-owned lock or an active QEMU effect.
Advisory owner leases are a later coordination feature and are separate from
this storage-first gate. The proposed 30-second token with renewal every
10 seconds applies only to that future coordination layer; storage does not
depend on client lease expiry.

Errors expose a stable `code`, affected IDs, `retryable`, `operation_id`,
and optional structured recovery actions. The shared taxonomy includes
`capability_unavailable`, `runtime_incompatible`, `checkpoint_incomplete`,
`external_dependency`, `operation_conflict`, `request_conflict`, and
`recovery_required`, plus existing `runtime_unavailable`,
`unsupported_checkpoint`, `readiness_timeout`, `resource_budget_exceeded`,
`lease_expired`, `in_use`, `not_found`, `cursor_invalid`, naming, and
state-conflict errors. Existing meanings remain unchanged. CLI output remains
JSON Lines with one terminal result. MCP tool errors must preserve machine
codes rather than returning only the human-readable message. Wire versioning
follows the published compatibility rules; this spec does not select a new
version number in advance.

Evidence export contains operation records, lineage, requested/effective
runtime policy, compatibility results, timestamped recipe and health
observations, and explicitly selected log or artifact references with size
and checksum. It does not label a branch correct because commands exited
successfully. It distinguishes observed, inherited, and unavailable evidence.

Existing background job IDs remain scoped to their source VM; current
metadata does not establish the new runtime identity contract.
A resumed child does not reuse the parent's host job records or claim an
inherited PID as a newly observed successful job. Full job adoption, durable
output collection, and command-level retry semantics are specified in
[Agent jobs, evidence, and handoff](2026-09-06-agent-jobs-evidence-and-handoff-design.md).
The checkpoint can carry provenance about those processes without claiming
that the current MCP job API can control them after a fork.

Evidence stays local by default. Manifests, disk objects, and saved memory
use private permissions. Arbitrary guest logs and RAM may contain secrets;
export includes only the caller-selected artifacts and does not claim that
generic redaction removes every credential. No upload is implicit.

## 9. Selecting and continuing an attempt

Forks are regular named VMs once created. A caller can continue the chosen
fork and remove unneeded siblings after collecting their evidence. The
source checkpoint remains available while anything references it.

An agent handoff consists of stable VM/runtime/checkpoint IDs, operation
status, evidence references, and a caller-written note. A portable handoff
file does not pin those objects and may contain stale references. A registered
local export or job reference protects only an existing known record; prune
does not discover or protect arbitrary copies of handoff files. Notes are
untrusted task data and do not grant capabilities. Stoat does not interpret an
agent's conversation or claim to reconstruct its reasoning.

Exporting changed source code can use a Git patch from a repository inside
the guest. That is application-level work and requires an explicit path and
base revision. This release does not promise a generic file diff, disk diff
merge, or promotion command that mutates the parent VM.

## 10. Existing implementation and integration

The inspected source baseline is `bbb62d8` (2026-09-06), after PR #74.
These are current facts, separate from the proposed requirements:

- `internal/core/clone.go` rejects a running source and creates qcow2
  overlays backed by the source disk. That dependency is not an immutable
  checkpoint object. Live-mode cloning copies configuration, not guest RAM.
- `internal/core/snapshot.go` and `internal/qemu/qmp.go` use QMP `savevm`
  and `loadvm` for running VMs, and `qemu-img` snapshots for stopped VMs.
  Runtime capture therefore has a starting point, but independent runtime
  forking has not been validated by this investigation.
- `internal/mcpsrv/tools_exec.go` stores background command output under
  `/run/stoat/jobs`; a reboot clears guest-side state. The host registry in
  `internal/mcpsrv/jobs.go` persists metadata, not a durable copy of all output.
- `internal/mcpsrv/server.go` currently returns only the mapped message in
  tool errors. The structured recovery contract above requires a change.
- `internal/qemu/args.go` currently attaches the writable work share and
  does not set user-network `restrict=on`. Disabling that export and adding
  the restricted backend are required implementation changes, not current
  Stoat guarantees.

The new storage graph belongs below CLI/TUI/MCP. All lifecycle and deletion
paths must respect it, including legacy clone/snapshot commands. Initially,
the new `fork` interface is explicit; existing clone semantics do not change
silently. A legacy disk dependency must be materialized into an immutable
view before it can participate in the new checkpoint graph.

Settled API decisions belong in `docs/design/`. This draft stays in the
repository's local, ignored `docs/specs/` area until reviewed. The unrelated
video drafts and documentation audit changes are not part of this design.

## 11. Delivery gates

| Gate | Required evidence |
|---|---|
| Disk isolation | Fork two children; change parent and each child; verify sibling and checkpoint bytes remain unchanged. |
| Deletion | Removing parent or one sibling does not break another; deleting a referenced checkpoint returns dependent IDs. |
| Runtime export | Capture a counter process and disk marker together; restore into a separately identified child; verify continuity and paired storage without reboot. |
| Same-VM suspend | Release QEMU after a durable save; resume the same runtime ID; reject mismatched RAM, machine type, or QEMU build. |
| Fork fallback | `require` fails before publication when unsupported; `auto` falls back only during preflight when the disk mechanism supports the current source state; resumed-restore errors never become cold-fork successes. |
| Identity | Cold child uses its own guest initialization without replaying general provisioning; resumed child has separate host resources and restricted egress. |
| Network | Against a controlled endpoint, verify that a resumed child's stale outbound session cannot send a second test request while a fresh host SSH connection works through its new port. This bounded test does not establish exactly-once behavior for arbitrary external services. |
| External state | Writable shares, passthrough, and missing media are rejected with actionable capability reasons. |
| Crash recovery | Interrupt capture and publication at each boundary; recover or expose partial state without a false ready checkpoint or broken parent. |
| Concurrency | Race two forks for a name/port, and fork against prune; only valid, uniquely reserved objects survive. |
| Retry | Replay the same request before and after a client crash; retrieve one operation and one resulting VM. |
| Agent parity | A JSON-only caller and an MCP caller observe equivalent semantic state, error codes, retryability, fallback reasons, and lineage, with an explicit mapping between the CLI envelope and MCP tool results. |
| Evidence | Export marks inherited health as inherited and missing job output as unavailable; it does not infer a successful test. |

Build order: prove disk publication on the supported guest profile; implement
the immutable store and stopped-source forks with typed operations; then prove
independent saved-runtime restore before adding its lifecycle continuation.
Expose CLI/MCP and lineage, then add TUI views and evidence export. Runtime
features stay unavailable if their gate fails. The disk-only path can ship
with that limitation stated.

## 12. Decisions still requiring validation

1. Choose the runtime export mechanism after a disposable live test. Compare
   an isolated copy of a complete internal-snapshot image with a coordinated
   disk checkpoint plus a QEMU state stream. Neither is accepted merely
   because `savevm` works in the original VM.
2. Prove cloud guest identity initialization without broad provisioning
   replay. Until then, advertise only guest profiles that pass the gate.
3. Confirm the proposed defaults: `auto` runtime selection, explicit resume,
   and restricted networking on new forks. These are product choices in
   this draft, not previously shipped policy.
4. Measure checkpoint time, memory-state size, and storage amplification on
   the supported host. Do not publish speed or savings claims before that.

## Sources

QEMU documents snapshots containing RAM, CPU/device state, and writable
disks, with limitations for removable media and some devices:
[VM snapshots](https://www.qemu.org/docs/master/system/images.html#vm-snapshots).
This establishes the underlying capability, not Stoat's fork compatibility.

QEMU describes point-in-time disk backups and the relationship between
active overlays and backing images:
[Live block operations](https://www.qemu.org/docs/master/interop/live-block-operations.html).

Saved-state compatibility depends on the machine type and hardware
configuration. Same-host, same-build support is the conservative policy
proposed here:
[Migration compatibility](https://www.qemu.org/docs/master/devel/migration/compatibility.html).

QEMU's user-network `restrict` option blocks guest-initiated host/external
traffic while leaving explicit forwarding rules effective:
[Network options](https://www.qemu.org/docs/master/system/invocation.html).
Its behavior with a resumed fork still requires the network gate.
