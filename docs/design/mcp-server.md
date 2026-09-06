# The MCP server

Decisions are settled here. This document is the source of truth for the
implementation; `core-api.md` §10 and `json-contract-draft.md` §7 hold the
original reasoning and should be read first, not re-argued.

For setup, access levels, and available tools, use the
[MCP reference](../reference/mcp.md). The [JSON contract](../reference/json.md)
defines the shared result types. This document records implementation decisions
and the original validation plan.

## 0. Settled, do not relitigate

| | |
|---|---|
| Language | Go, in the `stoat` binary. `github.com/modelcontextprotocol/go-sdk` v1.7.0. |
| Interface to stoat | `internal/mcpsrv` imports `internal/core` and `internal/cli/wire` directly. It does not shell out and does not read `~/.stoat` itself. |
| Layering | A thin mapping. **If a tool needs logic `core` does not have, the layering is wrong**, and the fix goes in `core`, not here. |
| Enforcement | Lives in code. Client-side approval is defence in depth and is never the boundary (MCP's human-in-the-loop is `SHOULD`, annotations are advisory). |
| v1 scope | The full four-class taxonomy. |
| Layout | `internal/mcpsrv/` in this repo, run with `stoat mcp`. The JSON contract check is a compile-time fact: both `stoat mcp` and `stoat --json version` read the same `wire.ContractVersion`. |
| `exec` | Gated by `agent_access`, one of four levels recorded per VM (§4). |

## 1. Layout

```
internal/mcpsrv/
  server.go        registers tools, builds the server, picks the transport
  contract.go      the contract constant this server speaks
  access.go        Level, requireAccess, the agent_access table
  guards.go        name, path, image id, index name, param name, flag-free
  ratelimit.go     per-tool and shared token buckets, as receiving middleware
  redact.go        secret redaction over wire values, as receiving middleware
  jobs.go          the jobs.toml registry for background exec
  install.go       stoat mcp install <client>
  doctor.go        stoat mcp doctor
  tools_read.go    list_vms, vm_status, list_images, list_recipes,
                   check_recipes, logs, doctor, plan_recipes, list_guests,
                   guest_info, recipe_schema, search_recipes
  tools_vm.go      create, start, stop, update, clone, snapshot, restore,
                   forward, wait, destroy, prune, apply_recipes
  tools_recipe.go  add_recipe, update_recipe, remove_recipe
  tools_guest.go   read_file, list_dir, stat, ps, svc_status, tail_log,
                   write_file, copy_to, copy_from, pkg_install, svc, useradd
  tools_exec.go    exec, exec_bg, job_status, job_output, job_kill, list_jobs
```

Binary resolution for `stoat mcp install`: the running binary's own absolute
path (`os.Executable`), never `$PATH` lookup, so the installed entry always
points at the binary that wrote it.

Every MCP client launches a stdio server as a subprocess, so `stoat mcp` (no
subcommand) defaults to `serve` over stdio. `--http 127.0.0.1:PORT` serves
streamable HTTP instead, for a client that cannot launch a subprocess;
`CheckLoopback` refuses any address that is not loopback, since this server
has no authentication.

## 2. Guards

`internal/mcpsrv/guards.go` ports the deterministic blocks one to one from
the design's original Python draft, as functions called at the top of each
handler. Pure functions, no I/O, so they are exhaustively testable in
`guards_test.go`.

| Guard | Rule |
|---|---|
| `checkVMName` | Must match stoat's own name rules: no paths, no traversal, no empty, no unicode lookalike separator. |
| `checkHostPath` | For `copy_to`/`copy_from` only. Resolves symlinks first, then requires the prefix `~/.stoat/shared/<vm>/`. A path resolving outside it is refused even when its string prefix matches a sibling directory. |
| `checkImageID` | Catalog IDs only. An absolute or relative path is rejected. |
| `checkGuestPath` | Every in-VM tool's path argument. Must be absolute; never resolved against `$HOME`. |
| `checkFlagFree` | A value splatted into argv as a positional (`forward` pairs, `check_recipes` names, `search_recipes`'s term) must not start with `-`. `forward(pairs=["--clear"])` otherwise reached kong as the clear flag. |
| `checkIndexName` | `add_recipe` takes an index name with an optional `@ref`; a URL, a path separator in the plain name, or a malformed ref is refused before it reaches `core`. `update_recipe` and `remove_recipe` take plain names. |
| `checkParamName` | A recipe param name set through `update`, bounded to the recipe contract's own grammar. |
| `checkSvcName` | A service name `svc` and `svc_status` render into a guest-file template as a positional argument. |
| `stripForbidden` | Drops `share`, `image`, `base`, `iso`, and `console_password` from any patch built from agent input, since `core.Patch` is a generic-map shape that makes them reachable otherwise. |

Rate limiting (`ratelimit.go`) is a per-tool token bucket (30, 0.5/s) and a
shared bucket (60, 2/s) across every tool, both checked before either is
charged, as receiving middleware. The MCP spec makes rate limiting a server
`MUST`.

**Never exposed as tools at all**: `share` as any parameter, BYO image
paths, `recipe new`, an ssh-command tool, and the global (no VM) `logs`.
These are absent, not gated; `TestForbiddenSurfacesAbsent` and
`TestNoForbiddenInputField` pin it.

## 3. Redaction

`redact.go` runs as receiving middleware, closest to the handler, after
every registered tool has built its result. It walks the result as generic
JSON and replaces the value of any field named `secrets`,
`console_password`, `authkey`, `password`, or `token` with `core.SecretSet`
or `core.SecretUnset`. This is the second layer: `wire.FromVMStatus` already
redacts a recipe's own secret params by the manifest's declared type, so a
DTO that forgets is still covered.

The `secrets` input of `update` is never echoed back: `wire.VM` has no
secrets field, so there is nothing to redact in the response, and the walk
above still covers a map or a list a future tool returns.

## 4. Agent access levels

`agent_access` replaces `allow_exec` in `vm.toml`, one of four levels. Each
level includes the tools of every level below it.

| Level | Tools |
|---|---|
| `none` | host-side only: `status`, `start`, `stop`, `snapshot`, `restore`, `logs`, `forward`, `update` |
| `observe` | `read_file`, `list_dir`, `stat`, `ps`, `svc_status`, `tail_log` |
| `manage` (default) | `write_file`, `copy_to`, `copy_from`, `pkg_install`, `svc`, `useradd`, `apply_recipes` |
| `exec` | `exec`, `exec_bg`, `job_status`, `job_output`, `job_kill`, `list_jobs` |

`stoat new --allow-exec` stays as a hidden alias for `--agent-access exec`;
`--allow-exec=false` maps to `manage`. An existing `vm.toml` with
`allow_exec = true` loads as `exec`, `false` as `manage`, so an old VM keeps
its meaning under the new field.

`requireAccess(vm, level)` gates every guest-touching tool. `core.Exec` does
not enforce it, because `core` is a library the CLI and TUI also call and a
blanket refusal there would be the wrong layer. A refusal names both levels:
`vm "dev" has agent_access = observe; needs manage`.

MCP's `update` tool may lower a VM's `agent_access` and never raise it.
Raising is CLI or TUI only, so an agent cannot grant itself more access than
a person gave it.

### The `manage` tools are fixed argv from the guest file

`pkg_install`, `svc`, `svc_status`, `tail_log`, and `useradd` render a fixed
argv from the guest definition's own verbs (`internal/guest`), so a VM at
`manage` can install a package, restart a service, read its log, and write a
config file without an open shell. `pkg_install` runs `pkg.setup` once, then
`pkg.install` plus the requested packages. `svc` runs `svc.<action>` for
`enable`, `start`, `stop`, or `restart`. `tail_log` runs `journalctl -u` on a
systemd guest, `tail` on the init's own log path otherwise, or `tail` on an
explicit `path`, with `lines` clamped to 2000. The first two escalate,
because their target comes from the guest file and carries no tool input.
An explicit `path` runs as the ssh user: a root read of any path an agent
names would hand `observe` more than `read_file` gives it.

### In-VM tools over ssh

Every tool that touches a guest wraps `sshx.Run`, which is the one place in
stoat that quotes an argv for the guest shell. A tool never concatenates a
value into a shell string; every value arrives as a positional argument.
Reads (`read_file`, `list_dir`, `stat`, `ps`) are annotated read-only;
everything else is class Execution (`destructiveHint`, `openWorldHint`).
`read_file`'s `max_bytes` is clamped to 1 MiB; binary content comes back
base64 with `encoding` set. `write_file` and `exec_bg` refuse with
`CodeNotRunning` on a stopped VM, the same as `exec`.

### Background jobs

`exec_bg` starts a command under a fixed shell runner and returns a job id
at once. The job id is `j-` plus 8 lowercase hex characters, chosen by the
host. The guest side is `/run/stoat/jobs/<id>/{out,err,exit,pid}`; a reboot
clears it and `job_status` then reports `unknown`, while the host record
stays. The host record is `jobs.toml` beside the VM's own `vm.toml`:

```toml
# written by stoat; do not edit
schema = 1

[jobs.j-9f3c1e2a]
argv    = ["sleep", "60"]
user    = "stoat"
cwd     = "/home/stoat"
dir     = "/run/stoat/jobs/j-9f3c1e2a"
started = "2026-09-04T10:00:00Z"
```

`list_jobs` reads this file only, so it works from the host without ssh and
on a stopped VM.

## 5. `stoat mcp install` and `stoat mcp doctor`

| Client | File | Key |
|---|---|---|
| `claude-code` | `~/.claude.json`, or `./.mcp.json` with `--project` | `mcpServers.stoat` |
| `claude-desktop` | `~/.config/Claude/claude_desktop_config.json` | `mcpServers.stoat` |
| `cursor` | `~/.cursor/mcp.json` | `mcpServers.stoat` |
| `vscode` | `./.vscode/mcp.json` | `servers.stoat` |

The written entry is `{"command": "<abs path to stoat>", "args": ["mcp"],
"cwd": "<cwd>"}`. `cwd` is the process's own working directory, so project
scope applies to the server the client launches. An existing `stoat` entry
is replaced; every other entry and every other top-level key is preserved;
the file is written to a temp file in the same directory and renamed.
`--print` writes the JSON to stdout instead, and touches no file.

`stoat mcp doctor` reports the contract version, the transport, the running
binary's path, and, for each client, whether it has an entry and whether
that entry's `command` matches the running binary. A stale entry launches a
different `stoat` than the one just installed, and that mismatch is what
this command exists to name.

## 6. Tools

The full tool table, its annotation class (`readOnlyHint`/`destructiveHint`/
`openWorldHint`), and its `agent_access` level are declared once in
`internal/mcpsrv/table_test.go`, which is the source of truth;
`TestAnnotationsMatchTable` asserts every registered tool matches it.

Every input struct's generated JSON schema sets `additionalProperties:
false`, so an unexpected parameter is rejected rather than silently ignored
(OWASP MCP guidance). Tool descriptions are the full text a client shows a
user and carry no hidden instructions; `TestNoEmDashInDescription` and
`TestEveryToolHasDescription` pin the surface.

`plan_recipes` is `apply --dry-run` as its own tool rather than a flag on
`apply_recipes`, because one tool cannot honestly declare `readOnlyHint`
both ways. `search_recipes` and `add_recipe` use the curated remote index;
`add_recipe` accepts an index name and an optional tag or branch ref,
including slash-containing branch refs, and never a repository URL.
`search_recipes` refuses a term that starts with a dash, through the same
`checkFlagFree` guard, so the term never reads as a flag.
`remove_recipe` has no `force` argument and refuses while a VM still lists
the recipe.

## 7. Testing

- Annotation table: every tool's `readOnlyHint`, `destructiveHint`,
  `openWorldHint` against the class table.
- Schema: `additionalProperties: false` on every input; none of `share`,
  `image`, `base`, `iso`, `console_password` reachable in any input; no em
  dash in any description.
- Guards: adversarial cases for every guard above: symlink escapes, `..`
  traversal, absolute paths, a path whose prefix matches the sandbox as a
  string but not as a directory, unicode tricks, empty and whitespace
  names.
- Rate limit: per-tool burst refused at 31; shared burst refused at 61
  across tools; neither bucket charged on a refusal.
- Redaction: a fixture VM with a sentinel secret; every tool's output is
  scanned for it.
- `requireAccess`: a table of every guest-touching tool against the four
  levels; `update` lowering succeeds, raising is refused; legacy
  `allow_exec` values map as documented.
- In-VM tools against a fake ssh: relative path refused, `max_bytes` clamp,
  `write_file` mode, `exec_bg` then `job_status` then `job_output` round
  trip, `job_kill`, `ps` cap, argv never shell-joined (a path with a space
  and a `;` arrives intact).
- Install: each client's config written into a temp home; existing
  unrelated entries preserved.
- An in-process client from the go-sdk drives the server over stdio for an
  end-to-end round trip.

## 8. Out of scope

Stated so nobody builds them by accident: authentication, multi-user, and
any tool that writes to the host outside `~/.stoat/shared/<vm>/`. A pty
session tool and a guest agent binary over vsock are candidates for their
own design after this one; the agent would also cover guests without sshd
(Windows) and streaming output.

## 9. Live gate

The Go server has not been booted against a real VM yet. It changes
`internal/sshx` and `internal/core/exec.go`, so a live boot exercising
`exec`, `write_file` and `exec_bg` gates the merge.

The findings below came from real VMs booted through the MCP tools, not from
mocks, against the Python build this package replaced (2026-08-04). A
throwaway `mcpgate` (alpine-cloud) and `mcplocked` were created, exercised
and destroyed; the data root was left exactly as found. They are facts about
the design, and the live boot above is what confirms the Go port keeps them.

- **create, start, wait**: reachable in 13.4s.
- **exec** returns real guest output, running as `uid=1001(stoat)`, the
  cloud-init account rather than root.
- **Quoting survives the whole chain**, which is the thing only a live test
  proves: `exec(argv=["touch", "/tmp/qt/my file"])` created ONE file. The
  argv goes through one quoter to the guest's ash, `sshx.Run` in the Go
  port, and any layer dropping a word boundary shows up here.
- **A nonzero guest status is DATA**: `sh -c 'exit 42'` returned
  `exit_code: 42` with no exception raised, matching the contract's rule
  that exec exits 0 whenever the command ran.
- **copy_to** into the sandbox landed and the guest read the content back.
  The path guard refused `/etc/passwd`, `~/.ssh/id_rsa`, and
  `~/.stoat/shared/<vm>/../../id_stoat`.
- **A VM without exec access is honoured on a real VM**: both `exec` and
  `copy_to` refused, before either reached the binary.

### The finding worth keeping: mapped-xattr works

The guest ran `ln -sf /etc /mnt/work/escape` inside its own writable share.
On the host, the entry is not a real symlink: QEMU stored the link as an
extended attribute instead of creating one. That is the traversal defence
§10.2 of core-api.md claimed, demonstrated rather than assumed.

It does not make `checkHostPath`'s symlink resolution redundant. §10.2 also
requires that `mapped-xattr` be DETECTED and degrade loudly rather than
silently falling back to `security_model=none`. If that detection ever
regresses, the guest's symlink becomes a real host symlink again and the
guard is the only thing left standing between an agent and the host
filesystem. Two independent mechanisms, neither relying on the other.
