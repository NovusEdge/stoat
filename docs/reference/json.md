# JSON output reference

`--json` provides machine-readable output for named VM commands. This document
defines the output contract. Project fan-out currently has one exception:
no-name `up`, `down`, and `apply` can write progress prose before their terminal
JSON result. Use a named VM invocation when the complete stdout stream must be
JSON.

This document is the contract. The human-facing CLI is documented in
[cli.md](cli.md).

`stoat mcp` exposes the same operations from inside the same binary and reuses
the `wire` DTO package for tool results. MCP does not use the CLI's JSON-lines
envelope, and some tool result DTOs differ from the corresponding CLI payload
(for example, MCP `wait` returns `healthy`, while CLI `wait --json` returns
`reached` and `waited_ms`). See the [MCP reference](mcp.md) for the tool
behavior and schemas.

```
stoat --json ls
{"v":3,"type":"result","cmd":"ls","ok":true,"data":{"vms":[...]}}
```

## The consumer contract for a named VM command

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

The parser uses the process exit code only when no result line was received.

Four rules make it work for a named VM command:

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

Project fan-out is a current limitation. With a project `stoat.toml` and no
VM name, `up`, `down`, and `apply` force their internal fan-out path and may
write human progress lines before the final `ProjectRun` result. A parser that
must use fan-out should capture the final result line only after handling this
known defect; prefer `stoat <command> <key> --json` for a clean stream. A
single-VM `down` result can also report `state: "running"` while QEMU is still
exiting; follow it with `stoat wait <key> --until stopped --json` when
termination must be confirmed.

Rule 3 prevents consumers from merging stdout and stderr to reconstruct a
result. Sequential reads from both pipes can deadlock when either buffer
fills. The result envelope carries the command error on stdout.

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
| `stage` | no | a recipe boundary during `apply`, or during `up` when it applies at boot |
| `log` | no | one line of guest output during `apply`, or during `up` when it applies at boot |

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
| `in_use` | a recipe is still listed by one or more VMs |
| `git_required` | a recipe operation needs Git on `PATH` |
| `not_running` | the operation needs a running VM |
| `already_running` | the operation needs a stopped VM |
| `no_disk` | the VM has no qcow2 (a live VM has none) |
| `immutable_field` | `update` was asked to change a field that cannot change |
| `disk_shrink` | a disk can only grow |
| `cannot_reach` | `wait` was asked for a state this VM can never reach |
| `unknown_log` | bad `--which` |
| `qemu_missing` | `qemu-system-x86_64` is not on `PATH` |
| `kvm_unusable` | `/dev/kvm` cannot be opened; the user is usually not in the `kvm` group |
| `qemu_start_failed` | qemu ran and refused to start the VM |
| `monitor_unreachable` | the VM's qemu monitor socket does not answer |
| `monitor_rejected` | qemu answered the monitor command with an error |
| `no_console_password` | the VM has no console password to type |
| `share_invalid` | the configured share is not a directory |
| `no_xattr` | the share's filesystem cannot store `user.*` extended attributes |
| `screenshot_failed` | qemu refused the screendump |
| `download_failed` | a mirror or a checksum file did not answer with the image |
| `download_stalled` | the download stopped producing bytes |
| `checksum_mismatch` | the downloaded bytes do not match the published digest |
| `no_such_image` | the requested image is not in the index |
| `timeout` | the deadline expired |
| `canceled` | the context was cancelled |
| `usage` | a bad flag, a missing argument, an unknown subcommand |
| `confirmation_required` | a destructive command was run without `-y` |
| `lock_out_of_date` | a project declaration is not pinned in `stoat.lock` |
| `internal` | anything unanticipated; the escape hatch |

**Codes are only ever added.** Never renamed, never repurposed, never removed.
A consumer MUST treat an unrecognized code as a generic failure rather than
crashing on it, because that is what makes adding one a non-breaking change.
The list above is `wire.Codes()`, which returns every declared code, sorted.

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
             "allow_exec":false,"agent_access":"manage","display":"vnc",
             "error":"only on a broken VM",
             "project":"/home/u/myrepo","key":"dev","project_missing":false}

VMStatus    {"name":"work",...VM fields...,"health":"ok","recipes_detail":[
             {"name":"xfce","applied":true,"version":"1.2","at":"...",
              "health":"unknown","params":{},"outputs":{}}]}

Image       {"id":"alpine-virt","os":"alpine","variant":"virt",
             "backend":"apkovl","file":"alpine-virt-3.24.1-x86_64.iso",
             "downloaded":true,"bytes":62914560,"bytes_exact":true,"byo":false}

Snapshot    {"tag":"clean","vm_state":true,"size_display":"203 MiB",
             "created_display":"2026-08-04 12:00:00"}

Check       {"name":"qemu-img","ok":false,"detail":"not found",
             "fix":["sudo","pacman","-S","qemu-img"],"optional":false}

PruneItem   {"class":"orphaned_image","path":"/home/u/.stoat/isos/old.iso"}

Recipe      {"name":"xfce","description":"XFCE desktop over SSH or at boot",
             "schema":2,"runtime":"sh","reboot":false,"depends":[],
             "params":[],"outputs":[],"health":null}

RecipeSchema {"name":"docker","description":"Docker engine and the compose plugin",
              "schema":3,"runtime":"sh","reboot":false,"depends":[],
              "params":[RecipeParam,...],"outputs":[RecipeOutput,...],
              "health":{"check":"docker info","timeout":"30s"}}
RecipeParam {"name":"channel","type":"enum","required":false,
             "default":"stable","values":["stable","test"],"help":"..."}
RecipeOutput {"name":"socket","help":"path of the socket"}
RecipeHealth {"check":"docker info","timeout":"30s"}

RecipeEntry {"name":"tailscale","description":"join a tailnet on boot",
             "scope":"global","source":"https://github.com/x/stoat-tailscale",
             "ref":"v1.2","commit":"9f3c1e2"}

RecipeRoot  {"path":"/home/u/.stoat/recipes","scope":"global"}

RecipeAdded {"name":"tailscale","source":"https://github.com/x/stoat-tailscale",
             "ref":"v1.2","commit":"9f3c1e2d4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d",
             "scope":"global"}

RecipeRemoved {"name":"tailscale","scope":"global"}

IndexEntry  {"name":"tailscale","source":"https://github.com/x/stoat-tailscale",
             "description":"join a tailnet on boot","os":["alpine"]}

RecipeIssue {"name":"xfce","reason":"xfce is not offered to fedora/cloudinit"}

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

MCPClient   {"client":"cursor","path":"/home/u/.cursor/mcp.json",
             "installed":true,"command":"/home/u/.local/bin/stoat",
             "current":true}

InitResult  {"path":"/home/u/myrepo/stoat.toml","project":"myrepo",
             "gitignore_updated":true}

Drift       {"field":"cpus","from":"2","to":"4","needs_restart":true}

ProjectStatusVM {"key":"dev","name":"myrepo-dev","state":"running",
                 "health":"ok","drift":[Drift,...],
                 "error":"only on an immutable-field mismatch"}
ProjectStatus   {"project":"myrepo","dir":"/home/u/myrepo",
                 "vms":[ProjectStatusVM,...]}

ProjectRunVM {"key":"dev","name":"myrepo-dev","status":"ok",
              "error":"only when status is error"}
ProjectRun   {"project":"myrepo","vms":[ProjectRunVM,...]}
```

`MCPClient.current` is false when the client's entry names a different
binary than the running one, which is the stale entry `mcp doctor` reports.

`VM.project` is the absolute directory of the `stoat.toml` that declared this
VM, and `VM.key` is the declaration key, both empty for a VM `stoat create`
made outside a project. `VM.project_missing` is true when that directory no
longer exists; the VM still lists and runs.

`ProjectStatusVM.state` is `missing` for a declared VM that does not exist
yet, otherwise a `VM.state` value. `drift` is empty when `error` is set: an
immutable-field mismatch (`image` or `disk`) stops the comparison before the
rest of the fields are checked.

`ProjectRunVM.status` is `ok`, `error` or `skipped`. `skipped` means a VM
earlier in declaration order failed and this one was never attempted;
`ProjectRun.vms` always lists every declared VM, in declaration order, so a
caller can see what did not run as plainly as what did.

`RecipeEntry` has `name`, `description`, `scope`, `source`, `ref`, and
`commit`. `scope` is one of `bundled`, `local`, `global`, or `project`; only
`global` and `project` entries carry `source`, `ref`, and the seven-character
commit prefix. `RecipeRoot` identifies each search root with `path` and
`scope`. `RecipeAdded` uses the same remote pin fields for add, lock, sync, and
update results, with the full resolved commit. `RecipeRemoved` contains only
the name and scope.

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

`agent_access` supersedes `allow_exec` with four levels: `none`, `observe`,
`manage`, and `exec`. Each level includes the operations allowed by the levels
below it. An explicitly stored `allow_exec = true` maps to `exec`. An explicit
`false` value or an absent key maps to `manage`. For compatibility, an absent
key still appears as `allow_exec:true` in the VM DTO. MCP permissions must use
`agent_access`; direct `stoat exec` and `stoat cp` commands do not enforce it.

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

`Image.byo` is explicit. Consumers must not derive it from an empty `id`.

`VM.display` is `"window"` or `"vnc"`, and `""` on a broken VM. `"window"`
means that the VM uses a QEMU window. `"vnc"` means that its screen is provided
by a VNC server on a Unix socket.

Consumers must not derive `display` from `mode` and `installed`. The value also
depends on the host. Without a graphical session, an uninstalled disk VM uses
VNC so that its installer remains accessible.

Stoat detects the host display from `DISPLAY`, `WAYLAND_DISPLAY`, and
`$XDG_RUNTIME_DIR/wayland-0`. `STOAT_GRAPHICAL=0` or `STOAT_GRAPHICAL=1`
overrides that detection. Host display availability is not a separate VM
field.

`display` identifies the surface, not its location. The payload does not
include the VNC socket path or an attach command. Run `stoat get <name>`
without `--json` to print the socket and a suitable viewer command.

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
| `init` | `InitResult` |
| `status` | `ProjectStatus` |
| `ls` | `{"vms":[VM,...]}` |
| `get` | `{"vm":VMStatus}` |
| `create` | `{"vm":VM}` |
| `update` | `{"vm":VM,"changed":["ram"],"applies_at":"now"}` |
| `up` (one VM) | `{"vm":VM}` (re-read after start, so `state` is authoritative) |
| `up`, `down`, `apply`, `wait`, `rm` (no VM, project scope) | `ProjectRun` |
| `down` (one VM) | `{"vm":VM}` |
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
| `recipe list` | `{"roots":[RecipeRoot,...],"recipes":[RecipeEntry,...]}` |
| `recipe show` | `{"recipe":RecipeSchema}` |
| `recipe new` | `{"path":"/home/u/.stoat/recipes/foo/"}` |
| `recipe add` | `{"name":"tailscale","source":"...","ref":"v1.2","commit":"9f3c1e2d4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d","scope":"global"}` |
| `recipe lock` | `{"recipes":[RecipeAdded,...]}` |
| `recipe sync` | `{"recipes":[RecipeAdded,...]}` |
| `recipe update` | `{"recipes":[RecipeAdded,...]}` |
| `recipe rm` | `{"name":"tailscale","scope":"global"}` |
| `recipe search` | `{"recipes":[IndexEntry,...]}` |
| `screenshot` | `{"vm":"work","path":"/home/u/.stoat/work/screenshots/2026-09-05T140302Z.png","bytes":48213,"width":1280,"height":800}` |
| `logs` (no VM) | `{"lines":[...]}` (stoat's own log) |
| `logs <vm>` | `{"vm":"work","which":"console","lines":[...]}` |
| `doctor` | `{"healthy":false,"checks":[Check,...]}` |
| `mcp doctor` | `{"contract":3,"version":"1.2.3","transport":"stdio","binary":"/home/u/.local/bin/stoat","clients":[MCPClient,...]}` |
| `mcp install` | `{"client":"cursor","path":"/home/u/.cursor/mcp.json","json":"{...}"}` |
| `version` | `{"version":"1.2.3","contract":3}` |
| `help` | `{"usage":"..."}` |
| `ssh` | **refused**, see below |

The table above is for the CLI's `--json` results. MCP uses the same DTO
package but has tool-specific payloads: for example, MCP `copy_to` and
`copy_from` return `CopyResult` (`vm`, `local`, `remote`, `to_remote`), MCP
`forward` returns `ForwardList` (`forwards`), and MCP `apply_recipes` returns
`ApplyResult` (`vm`, `recipes_detail`). Consult the MCP tool schema for those
fields rather than assuming the CLI row applies.

`get` returns `{"vm":VMStatus}`: `VMStatus` embeds the VM fields directly;
only the outer get result has the `vm` member. `recipes` remains the compatible
string list, while `recipes_detail` adds stored per-recipe state. `health` is the stored aggregate
(`ok`, `failed`, or `unknown`); it is not a live SSH check. Every detail's
`params` and `outputs` is an object, even when empty. Secret parameters are
`<set>` or `<unset>` and are never emitted as their value.

`recipe show` and `recipes` use the same `RecipeSchema` projection. Parameters
and outputs are named arrays sorted by name. A recipe without a health check
has `health:null`; all list fields are `[]`, never `null`.

All `recipe` subcommands report `"cmd":"recipe"`, not the full subcommand
path, and all `guest` subcommands report `"cmd":"guest"`. Distinguish them by
which fields `data` carries.

`recipe list` reports every valid manifest in shadow order. Each row names its
scope (`bundled`, `local`, `global`, or `project`); only remote `global` and
`project` rows carry source, ref, and the seven-character commit prefix. The
`roots` list gives the search order and the scope label for each root.

`up`, `down`, `apply`, `wait` and `rm` report `ProjectRun` only when they run
at project scope with no VM argument; given a VM, each keeps its one-VM shape
from the row above. `stoat.toml`'s `[project]` fan-out is the only thing that
changes `data`'s shape; the command's own `cmd` name does not.

Fields worth knowing about:

- **`update.changed`** names the fields that actually changed, in wire naming
  (`ssh_port`, not `SSHPort`). A flag you did not pass does not appear, and
  the field it names is untouched.
- **`applies_at`** is `now` or `next_start`, and appears on `update` and on a
  `forward` that changed the configuration. A forward saved on a running VM is
  not active until the next start. A `forward` that only displays the current
  configuration reports `active` without `applies_at`.
- **`check-recipes.applicable`** is emitted explicitly even though it equals
  `issues == []`. Consumers must use this field instead of deriving the value.
- **`apply.skipped_reason`** distinguishes "recipes ran" from "there was
  nothing to run" without reading English. It is `""` when the apply ran. A
  cloud VM's recipes ran at first boot, and a second run already holding the
  VM's lock is the other case; both come back with `applied: []` and a reason.
  `applied` is always a list, never a bool.
- **`provision`** is a hidden alias of `apply` and reports `"cmd":"apply"`.
  It has no `data` shape of its own.
- **`doctor.healthy`** reports host readiness. The enclosing result envelope
  uses `ok` for command success.

### `exec` and non-UTF-8 output

A guest's stdout can contain arbitrary bytes. Go's JSON encoder replaces
invalid UTF-8 with U+FFFD and loses the original bytes. Stoat therefore uses
base64 for a stream that is not valid UTF-8:

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

`pull`, `apply` and `up` emit non-terminal events before their result. `up`
emits the `stage` and `log` events of the apply it runs after boot, with
`"cmd":"up"`, and its result comes after that apply has finished. `apply
--dry-run` emits none: it computes the plan host-side and runs nothing.

```
{"v":3,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":41943040,"total":62914560,"percent":66}}
{"v":3,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":62914560,"total":62914560,"percent":100}}
{"v":3,"type":"result","cmd":"pull","ok":true,"data":{"id":"alpine-virt","downloaded":true,"verified":true,"checksum_available":true}}
```

`progress` fires only when the percentage changes, not per read.

`apply` wraps each appended line of the recipe log as a `log` event, and emits
a `stage` event at each recipe boundary:

```
{"v":3,"type":"stage","cmd":"apply","data":{"recipe":"xfce"}}
{"v":3,"type":"log","cmd":"apply","data":{"line":"+ apk add xfce4"}}
```

The stage boundaries are real, read out of the markers the provisioner already
writes, rather than a percentage nobody can compute.

Lines are flushed as they are produced, so a buffered stdout does not turn
streaming into "silent until exit".

## Versioning

`"v"` is an integer **contract** version, not the build version. It is `3`.

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

**There is no v1 compatibility path.** Stoat does not read or serve the old
format. A v1 consumer refuses a v2 binary, and a v2 consumer refuses a v1
binary. The startup version check reports both contract versions.

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

**v3.** `recipe list` changed shape for remote recipes. `dir` became `roots`,
a list of `{path, scope}` in search order, and `recipes` became a list of
`RecipeEntry` objects rather than names. A consumer that read
`data.recipes[]` as strings reads `data.recipes[].name` instead.

The same version also adds `agent_access` to `VM`, additive alongside
`allow_exec`, and moves the MCP server from a separate Python process into
`stoat mcp` in this binary. Neither change removes or repurposes a field, so
neither bumped the version on its own; they are noted here only because they
landed in the same branch as the `recipe list` change.

The project-file release added `init` and `status` commands, `--project` on
`ls`, and a no-argument fan-out on `up`, `down`, `apply`, `wait` and `rm` at
project scope. It also added `project`, `key` and `project_missing` on `VM`,
and five MCP tools (`project_status`, `project_up`, `project_down`,
`project_apply`, `project_wait`) alongside `start`, `stop`, `apply_recipes`
and `wait`, which keep their existing inputs and outputs. All additions; the
contract stays 3.
