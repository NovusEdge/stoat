# JSON Output Reference

`--json` turns any subcommand into a machine interface. It exists because
stoat's MCP server is a separate Python process that reaches `internal/core`
only by running this binary and reading its output, so everything a caller
would otherwise regex, guess at, or reconstruct is defined here instead.

This document is the contract. The human-facing CLI is documented in
[cli.md](cli.md).

```
stoat --json ls
{"v":2,"type":"result","cmd":"ls","ok":true,"data":{"vms":[...]}}
```

## The consumer contract, in one page

```python
proc = subprocess.run(argv, stdout=PIPE, stderr=DEVNULL)
result = None
for line in proc.stdout.splitlines():
    obj = json.loads(line)
    if obj.get("type") == "result":
        result = obj
if result is None:
    raise StoatCrashed(proc.returncode)      # died before answering
if not result["ok"]:
    raise StoatError(result["error"]["code"], result["error"]["message"])
return result["data"]
```

Note what this does **not** do: branch on the exit code for anything except
"was there a result at all". That is the intended shape.

Four rules make it work:

1. **Every line of stdout is one JSON object.** Nothing else is ever written
   to stdout in `--json` mode, including a recipe's own output, which is
   wrapped as `log` events rather than passed through.
2. **Exactly one line has `"type":"result"`, and it is last.** Read to EOF and
   take it.
3. **Errors are on stdout too**, in that same result line. stderr carries only
   things that are not part of this contract: Go panic traces, and stray
   output from `qemu-img` or `ssh` that escaped capture. Do not parse stderr.
4. **Either you get a result line, or the process died.** The second case is
   detectable as "exit code is nonzero and no result line was seen".

Rule 3 is not a preference. A consumer that must merge two pipes to
reconstruct one result will eventually interleave them wrong, and a naive
`subprocess` read of two pipes in sequence deadlocks when either buffer fills.
The error envelope answers the question that was asked.

## The envelope

Every line:

| Field | Type | Present |
|---|---|---|
| `v` | int | always |
| `type` | string | always |
| `cmd` | string | always |
| `ok` | bool | `result` lines only |
| `data` | object | on success, and on non-terminal events |
| `error` | object | on failure only |

`data` and `error` are mutually exclusive on a result line. `ok` is carried
separately rather than derived from which one is set, so that a consumer
branching on `ok` and one branching on `"error" in obj` can never disagree.

### Event types

| `type` | Terminal | Meaning |
|---|---|---|
| `result` | yes | the answer; exactly one per invocation, always last |
| `progress` | no | a download's byte count (`pull`) |
| `stage` | no | a recipe boundary during `apply` |
| `log` | no | one line of guest output during `apply` |

**A consumer MUST ignore any `type` it does not recognize.** New non-terminal
types can be added without a version bump.

### The error object

| Field | Type | Present |
|---|---|---|
| `code` | string | always |
| `message` | string | always, human-readable, not machine-parseable |
| `subject` | string | reserved, see below |
| `kind` | string | reserved, see below |

Branch on `code`. Never parse `message`.

`subject` and `kind` are defined in the envelope but **no command emits them
today**, so a consumer will not see them. They exist for a later change that
names what an error is about (`subject:"work"`, `kind:"vm"`) without putting
it in the prose. Because they are additive and optional, that change will not
bump the contract version. Do not write code that requires them.

## Error codes

| Code | Meaning |
|---|---|
| `not_found` | no such VM |
| `broken` | the VM's `vm.toml` will not parse |
| `name_taken` | a VM by that name already exists |
| `invalid_spec` | the request itself is malformed |
| `image_not_downloaded` | the image exists in the catalog but not on disk |
| `recipe_not_applicable` | a named recipe cannot run on this VM |
| `not_running` | the operation needs a running VM |
| `already_running` | the operation needs a stopped VM |
| `no_disk` | the VM has no qcow2 (a live VM has none) |
| `immutable_field` | `update` was asked to change a field that cannot change |
| `disk_shrink` | a disk can only grow |
| `cannot_reach` | `wait` was asked for a state this VM can never reach |
| `applied_at_boot` | reserved; no command emits it. A cloud VM whose recipes ran at boot answers `ok:true` with `applied: []` |
| `unknown_log` | bad `--which` |
| `timeout` | the deadline expired |
| `canceled` | the context was cancelled |
| `usage` | a bad flag, a missing argument, an unknown subcommand |
| `confirmation_required` | a destructive command was run without `-y` |
| `internal` | anything unanticipated; the escape hatch |

**Codes are only ever added.** Never renamed, never repurposed, never removed.
A consumer MUST treat an unrecognized code as a generic failure rather than
crashing on it, because that is what makes adding one a non-breaking change.

## Exit codes

`0` success, `1` runtime failure, `2` usage error. Nothing else, and no code
per error kind: the envelope carries the error with more precision than a byte
can, and a new error kind must not need a new number.

Two commands deliberately differ under `--json`:

- **`exec` exits 0 whenever the guest command RAN**, whatever the guest
  returned. The guest's status is in `data.exit_code`. Exit 1 means stoat
  could not run it at all. Without `--json`, `exec` passes the guest's status
  through like ssh does, which is right for a human running
  `stoat exec vm make test && deploy` and fatal for a machine, since a guest
  exiting 2 would be indistinguishable from a stoat usage error.
- **`doctor` exits 0 with `healthy:false`.** It succeeded at checking; the
  host is unhealthy, which is the answer rather than a failure to produce one.

## What `--json` implies

- **Non-interactive, always.** `rm` without `-y` fails with
  `confirmation_required` rather than prompting. A boundary that can be
  crossed by a process that answers a prompt is not a boundary.
- **No progress prose.** Anything worth saying is a field.
- **No ANSI colour**, regardless of terminal detection.
- **`-q` becomes meaningless** and is accepted as a no-op.

### Where the flag may appear

`--json` is scanned out of argv before parsing, so a usage error can still
answer in the contract. It is recognized anywhere EXCEPT after `exec`'s VM
name, because everything there belongs to the guest:

```
stoat --json exec work ls -la      # stoat's
stoat exec --json work ls -la      # stoat's
stoat exec work ls --json          # the guest's, untouched
```

## Types

Repeated across commands. Every list-valued field is `[]` when empty, never
`null`.

```json
VM          {"name":"work","os":"alpine","mode":"cloud","backend":"cloudinit",
             "state":"stopped","cpus":4,"ram_mb":4096,"disk":"8G",
             "share":"/home/u/src","recipes":["xfce"],
             "ssh_port":2200,"ssh_user":"stoat","installed":false,
             "forwards":[{"host_port":8080,"guest_port":80}],
             "allow_exec":true,"display":"vnc",
             "error":"only on a broken VM"}

Image       {"id":"alpine-virt","os":"alpine","variant":"virt",
             "backend":"apkovl","file":"alpine-virt-3.24.1-x86_64.iso",
             "downloaded":true,"bytes":62914560,"bytes_exact":true,"byo":false}

Snapshot    {"tag":"clean","vm_state":true,"size_display":"203 MiB",
             "created_display":"2026-08-04 12:00:00"}

Check       {"name":"qemu-img","ok":false,"detail":"not found",
             "fix":["sudo","pacman","-S","qemu-img"]}

PruneItem   {"class":"orphaned_image","path":"/home/u/.stoat/isos/old.iso"}

Recipe      {"name":"xfce","description":"XFCE desktop over SSH or at boot",
             "reboot":false,"depends":[],"runtime":"sh"}

RecipeIssue {"name":"docker","reason":"docker is not offered to debian/cloudinit"}

ApplyPlan   {"name":"xfce","action":"run","reason":"never applied",
             "version":"1.2"}

Guest       {"name":"fedora","init":"systemd","shell":"/bin/bash",
             "installer":"","default_backend":"cloudinit",
             "default_ssh_user":"stoat","escalate":["sudo","-n"],
             "capabilities":["dnf","systemd"],"aliases":["rpm-family"],
             "filename_hints":["fedora"],"seed_packages":[],
             "pkg":{"setup":"","install":["dnf","install","-y"],
             "env":{},"runtime_packages":{"python3":"python3"}},
             "svc":{"enable":"systemctl enable {name}", ...},
             "cmd":{},"backend":{"cloudinit":{"skip_9p":false}},
             "source":"bundled"}
```

`state` is one of `stopped`, `running`, `broken`. `error` appears only on a
broken VM.

`os` and `backend` can be **empty strings** on a VM created before those
fields were recorded in `vm.toml`. The VM is otherwise usable, but nothing can
answer what guest OS it runs, so treat an empty `os` as unknown rather than as
a value.

`allow_exec` is `true` for every VM that predates the field, not Go's zero
value: an absent `allow_exec` key in `vm.toml` is read as true, so a caller
does not need to special-case an old VM. It is a recorded fact, not an
enforced one: `stoat exec`/`cp` do not check it, so a consumer that must
refuse exec on a VM with `allow_exec:false` (the MCP server) has to check it
itself before calling.

`Snapshot.size_display` and `created_display` are named that way because they
are qemu's own formatted table output. They are opaque. Do not parse them.

`Recipe.reboot` says the guest needs a restart before that recipe's effect is
visible. A caller that runs `apply` and then waits for `reachable` can see the
sshd that is about to go down, so it must account for the reboot itself.
`Recipe.depends` names recipes that run first. `apply` orders the run on its
own, so a caller reads `depends` to report the order, never to sort by it.

`ApplyPlan.action` is `run` or `skip`, and `reason` is human text whose wording
is not part of the contract. `version` is the recipe version already applied,
absent when the recipe never ran.

`Image.file` is a bare filename under the data root's `isos/`, never an
absolute path. That is a guarantee, not an accident.

`Image.byo` is emitted explicitly rather than left to be derived from an empty
`id`, because a structural rule a consumer has to re-derive is one a consumer
will eventually re-derive wrong.

`VM.display` is `"window"` or `"vnc"`, and `""` on a broken VM. It says which
surface the VM's screen appears on: `"window"` for a real QEMU window, and
`"vnc"` for every other VM, whose screen goes to a VNC server bound on a unix
socket. It is emitted for the same reason as `Image.byo`: it is exactly the
kind of rule a consumer re-derives and then keeps applying after stoat changes
it.

That has now happened once. `display` is **not** derivable from `mode` and
`installed`: it also depends on the host stoat is running on. A window needs a
graphical session, QEMU exits 1 rather than degrading when there is none, so on
a host with no session an uninstalled disk VM gets `"vnc"` too, and its OS
installer is driven over the socket. A consumer that computed `display` from
`mode` and `installed` would tell its human to look at a window that will never
open, on a machine with no screen to open it on.

The host answer comes from `DISPLAY`, `WAYLAND_DISPLAY` and
`$XDG_RUNTIME_DIR/wayland-0`, and `STOAT_GRAPHICAL=0`/`=1` in stoat's
environment overrides it. It is a property of the machine running the CLI, not
of the VM, so it is not carried as a field of its own: a consumer that wants to
know why a VM is on VNC is asking about the host it is talking to, and every VM
in one response answers the same way.

`display` names the surface and never its location. The socket path is not on
the wire and neither is a rendered command for opening it; a command would
just embed the same absolute host path behind a friendlier field name. An
agent cannot run a GUI viewer anyway. A consumer that needs to tell a human
where to look runs `stoat get <name>` without `--json`, which prints the
socket and an attach command for a viewer installed on that machine.

`Guest.source` is `"bundled"`, `"user"`, or `"bundled+user"` for a user file
merged over a bundled one. `Guest.svc` and `Guest.cmd` are template strings,
not commands to run directly: `{name}` renders to the service/argument, see
`docs/reference/guest.md`. `Guest.backend` passes each `[backend.<name>]`
table through opaque; only the backend package that owns `<name>` defines its
keys.

### What is deliberately absent

- **Host paths.** `core.VM` carries six absolute host paths (its disk, console
  log, monitor socket, and so on). None reach the wire. The DTO constructor
  does not read that field at all, so no future JSON tag can leak it. This
  includes the VNC socket: see `display` above.
- **`console_password`.** A console password is useless unless shown to a
  human at a console, and it must never reach a wire format.
- **`iso` and `base`.** `base` is an absolute host path. Both are omitted
  until a caller has a concrete need.

These are pinned by a test that runs the real CLI and greps its whole output,
so a leak fails the build rather than shipping.

## Per-command `data`

| `cmd` | `data` |
|---|---|
| `ls` | `{"vms":[VM,...]}` |
| `get` | `{"vm":VM}` |
| `create` | `{"vm":VM}` |
| `update` | `{"vm":VM,"changed":["ram"],"applies_at":"now"}` |
| `up` | `{"vm":VM}` (re-read after start, so `state` is authoritative) |
| `down` | `{"vm":VM}` |
| `wait` | `{"vm":"work","until":"reachable","reached":true,"waited_ms":4210}` |
| `rm` | `{"name":"scratch","deleted":true}` |
| `clone` | `{"vm":VM,"source":"work","forwards_copied":false}` |
| `exec` | `{"vm":"work","exit_code":1,"stdout":"...","stderr":"..."}` |
| `ssh-command` | `{"argv":["ssh","-p","2200",...]}` |
| `cp` | `{"vm":"work","direction":"to_guest","local":"/home/u/f","remote":"/tmp/f"}` (`local` is always resolved to an absolute path, even if given relative or `~`-prefixed) |
| `forward` (show) | `{"vm":"work","forwards":[...],"active":true}` |
| `forward` (set/clear) | `{"vm":"work","forwards":[...],"active":false,"applies_at":"next_start"}` |
| `images` | `{"images":[Image,...]}` |
| `pull` | `{"id":"alpine-virt","downloaded":true,"verified":true,"checksum_available":true}` |
| `snapshot` (list) | `{"vm":"work","snapshots":[Snapshot,...]}` |
| `snapshot` (act) | `{"vm":"work","tag":"clean","action":"restore"}` |
| `prune` | `{"dry_run":true,"items":[PruneItem,...]}` |
| `apply` | `{"vm":"work","applied":["xfce"],"skipped_reason":""}` |
| `apply --dry-run` | `{"vm":"work","dry_run":true,"plan":[ApplyPlan,...]}` |
| `recipes` | `{"recipes":[Recipe,...]}` |
| `check-recipes` | `{"applicable":false,"issues":[RecipeIssue,...]}` |
| `guest ls` | `{"guests":[Guest,...]}` |
| `guest show` | `{"guest":Guest}` |
| `recipe list` | `{"dir":"...","recipes":["xfce"]}`, see note below |
| `recipe new` | `{"path":"/home/u/.stoat/recipes/foo.alpine.sh"}` |
| `screenshot` | `{"vm":"work","path":"/home/u/.stoat/work/screenshots/2026-09-05T140302Z.png","bytes":48213,"width":1280,"height":800}` |

Both `recipe` subcommands report `"cmd":"recipe"`, not `"cmd":"recipe list"`,
and both `guest` subcommands report `"cmd":"guest"`. Distinguish them by which
fields `data` carries.

`recipe list` is "every file in the recipes directory", which is not the same
as "every recipe you can use": it currently includes the `.bak` files the
one-time manifest upgrade left behind, and those are not applicable to any VM.
Use `recipes` (which filters by OS and backend) to find something a VM can
actually run; use `recipe list` only to find a file to edit.
| `logs` (no VM) | `{"lines":[...]}` (stoat's own log) |
| `logs <vm>` | `{"vm":"work","which":"console","lines":[...]}` |
| `doctor` | `{"healthy":false,"checks":[Check,...]}` |
| `version` | `{"version":"1.2.3","contract":2}` |
| `help` | `{"usage":"..."}` |
| `ssh` | **refused**, see below |

Fields worth knowing about:

- **`update.changed`** names the fields that actually changed, in wire naming
  (`ssh_port`, not `SSHPort`). A flag you did not pass does not appear, and
  the field it names is untouched.
- **`applies_at`** is `now` or `next_start`, and appears on `update` and on a
  `forward` that CHANGED something. It is the field that must exist: a forward
  saved on a running VM is not live yet, and "saved but not live" must never
  be readable as "refused". A `forward` that only shows reports `active`
  without `applies_at`, since it changed nothing to apply.
- **`check-recipes.applicable`** is emitted explicitly even though it equals
  `issues == []`, because a consumer reading an empty list and guessing is a
  consumer that will one day guess wrong.
- **`apply.skipped_reason`** distinguishes "recipes ran" from "there was
  nothing to run" without reading English. It is `""` when the apply ran. A
  cloud VM's recipes ran at first boot, and a second run already holding the
  VM's lock is the other case; both come back with `applied: []` and a reason.
  `applied` is always a list, never a bool.
- **`provision`** is a hidden alias of `apply` and reports `"cmd":"apply"`.
  It has no `data` shape of its own.
- **`doctor.healthy`**, not `ok`: the envelope already owns `ok`, and two
  differently-scoped `ok` fields one level apart is a trap.

### `exec` and non-UTF-8 output

A guest's stdout is arbitrary bytes. Go's JSON encoder silently replaces
invalid UTF-8 with U+FFFD, which is lossy and silent, the bad combination. So
when a stream is not valid UTF-8 it is carried as base64 instead:

```json
{"stdout_base64":"...","stdout_encoding":"base64","exit_code":0}
```

The plain field is omitted when its base64 counterpart is present, and vice
versa. Encoding is reported **per stream**, because stdout and stderr can
independently be invalid. An absent `*_encoding` means UTF-8.

### `ssh` is refused

`stoat --json ssh <vm>` is a usage error. `ssh` replaces the stoat process
image, so there is no "after" in which to write the terminal result line, and
faking one would break the exactly-one-result guarantee everywhere. Use
`exec` for a command, or `ssh-command` for the argv to run yourself.

## Streaming

`pull` and `apply` emit non-terminal events before their result. `apply
--dry-run` emits none: it computes the plan host-side and runs nothing.

```
{"v":2,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":41943040,"total":62914560,"percent":66}}
{"v":2,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":62914560,"total":62914560,"percent":100}}
{"v":2,"type":"result","cmd":"pull","ok":true,"data":{"id":"alpine-virt","downloaded":true,"verified":true,"checksum_available":true}}
```

`progress` fires only when the percentage changes, not per read.

`apply` wraps each appended line of the recipe log as a `log` event, and emits
a `stage` event at each recipe boundary:

```
{"v":2,"type":"stage","cmd":"apply","data":{"recipe":"xfce"}}
{"v":2,"type":"log","cmd":"apply","data":{"line":"+ apk add xfce4"}}
```

The stage boundaries are real, read out of the markers the provisioner already
writes, rather than a percentage nobody can compute.

Lines are flushed as they are produced, so a buffered stdout does not turn
streaming into "silent until exit".

## Versioning

`"v"` is an integer **contract** version, not the build version. It is `2`.

It bumps only for a removal or a meaning change: a field deleted, a unit
changed, an error code split or repurposed, or `result` ceasing to be last.
**Additions never bump it.**

### History

**v2.** The recipe system moved from flat files whose target OS was encoded in
the filename (`xfce.alpine.sh`) to directories with a `recipe.toml` manifest.
`Recipe` lost `label`, `target_os` and `shared`, and gained `description`;
recipe names are now bare (`xfce`, not `xfce.alpine.sh`), so every name a
consumer passes to `create --recipes`, `apply --only` or `check-recipes`
changed shape too.

**There is no v1 compatibility path, deliberately.** Nothing in stoat reads
the old format, and nothing here serves it. A consumer built for v1 refuses to
start against a v2 binary and the reverse also holds, which is what the
startup version check is for: the failure is a clear refusal naming both
versions rather than a field quietly missing three calls later.

A data root still holding v1 recipe files is not migrated. The v2 installer
writes its directories alongside them and ignores the rest, so the stale files
are inert but visible. Moving them aside is a one-line manual step.

A consumer may rely on:

- `v`, `type`, `cmd` on every line
- exactly one `result` line, last
- `[]` for an empty list, never `null`
- an unrecognized `type` being safe to skip
- an unrecognized `code` being a generic failure

A consumer may **not** rely on: field order, the exact text of `message`, the
contents of any `*_display` field, or the absence of fields it does not know.
