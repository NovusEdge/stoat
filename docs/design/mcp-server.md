# The MCP server: build spec

Decisions are settled here. This document is the source of truth for the
implementation; `core-api.md` §10 and `json-contract-draft.md` §7 are the
reasoning behind it and should be read first, not re-argued.

Branch: `mcp-server`. Contract: [../reference/json.md](../reference/json.md).

## 0. Settled, do not relitigate

| | |
|---|---|
| Language | Python + fastmcp, a separate process. Chosen twice by the owner with a researched recommendation for Go in front of them. |
| Interface to stoat | Run the `stoat` binary with `--json` and read JSON Lines. Never link Go, never read `~/.stoat` directly. |
| Layering | A thin mapping. **If a tool needs logic `core` does not have, the layering is wrong**, and the fix goes in `core`, not here. |
| Enforcement | Lives in code. Client-side approval is defence in depth and is never the boundary (MCP's human-in-the-loop is `SHOULD`, annotations are advisory). |
| v1 scope | The full four-class taxonomy. |
| Layout | `mcp/` in this repo, with a startup contract-version check. |
| `exec` | Allowed by default, with an optional per-VM opt-out. |

## 1. Prerequisites in the Go CLI

Both land before the Python, on this branch.

### 1.1 `cp` takes explicit flags under `--json`

`json-contract-draft.md` §7.3 item 1, the most important single item in §7.
Today `cp` smuggles a host path inside a compound `<vm>:<path>` argument. For
the server to know which side is a host path it would have to reimplement the
colon split and the both-or-neither rejection, and any divergence is a hole.
Worse, a host path legitimately containing a colon is ambiguous.

Add, alongside the existing positional spelling, which stays for humans:

```
stoat --json cp --vm work --direction to --local /abs/host --remote /tmp/x
stoat --json cp --vm work --direction from --remote /tmp/x --local /abs/host
```

`--direction` is an enum, `to` or `from`. The flag form and the positional
form are mutually exclusive; giving both is a usage error. The result must
**echo back the resolved absolute `local` path it acted on** (§7.3 item 2), so
the server can post-verify that what it authorised is what happened.

### 1.2 `core.Spec.AllowExec`

Recorded per VM in `vm.toml` as `allow_exec`. **Default true**, including for
every VM that predates the field, so nothing breaks and stoat stays useful to
an agent. `core.Exec` does NOT enforce it: enforcement is the server's job and
`core` is a library the TUI and CLI also call. `core.VM` and the JSON DTO
expose it so the server can read it; `create --allow-exec=false` sets it.

The reason it exists at all: a user who wants one VM an agent may never run
code in should be able to say so once, at create time, rather than trusting
every future caller.

## 2. Layout

```
mcp/
  pyproject.toml
  README.md
  stoat_mcp/
    __init__.py
    client.py     # subprocess + envelope decoding. The ONLY place that runs stoat.
    errors.py     # StoatError, code constants mirroring json.md
    guards.py     # the deterministic blocks. No I/O, pure functions, heavily tested.
    server.py     # fastmcp tool definitions. Thin: validate, call client, return.
  tests/
    test_client.py
    test_guards.py
    test_server.py
```

Binary resolution: `$STOAT_BIN`, else `shutil.which("stoat")`. Fail loudly at
startup if neither resolves.

**Startup contract check.** Call `stoat --json version` once and compare
`data.contract` against the `EXPECTED_CONTRACT` constant. Refuse to start on a
mismatch with a message naming both versions. A stale binary must fail
immediately, not as a confusing `KeyError` three tools later.

## 3. `client.py`

One function does all subprocess work:

```python
def run(*args: str, timeout: float | None = None) -> dict: ...
```

Rules, each of which is the contract in json.md and not a choice:

1. `stdout=PIPE`, `stderr=DEVNULL`. Never parse stderr.
2. Read every line, `json.loads` each, keep the one with `type == "result"`.
3. No result line and a nonzero exit means the process died: raise
   `StoatCrashed(returncode)`.
4. `ok: false` raises `StoatError(code, message)`. Callers branch on `code`,
   never on `message`.
5. **Ignore any unrecognized `type`.** New event types must not break us.
6. Never branch on the exit code for anything except "was there a result".

Streaming tools (`pull`, `apply`) additionally surface `progress`, `stage` and
`log` events. Expose them through a callback, not by buffering the whole run.

## 4. `guards.py`

Pure functions, no I/O, so they are exhaustively testable. Every one of these
is enforced regardless of what the client does.

| Guard | Rule |
|---|---|
| `check_vm_name` | Must match stoat's own name rules and resolve to a managed VM. No paths, no traversal, no empty. |
| `check_host_path` | For `copy_to`/`copy_from` only. Resolve with `os.path.realpath` (after symlinks), require the prefix `~/.stoat/shared/<vm>/`, reject anything else. |
| `check_image_id` | Catalog IDs only. An absolute or relative path is rejected: §7.1 #4. |
| `rate_limit` | A token bucket per tool. The MCP spec makes rate limiting a server `MUST`. |

**Never exposed as tools at all** (§7.1): `share` as any parameter, BYO image
paths, `recipe new`, `ssh-command`, and the global (no VM) `logs`. These are
not gated, they are absent.

`share` is asymmetric on purpose: it is fine as OUTPUT on a VM object, so an
agent can see a share exists. It is never accepted as INPUT.

## 5. Tools

Annotations are declared honestly AND enforced independently, since a client
may ignore them.

| Class | Tools | `readOnlyHint` | `destructiveHint` |
|---|---|---|---|
| Read-only | `list_vms`, `vm_status`, `list_images`, `list_recipes`, `check_recipes`, `logs`, `doctor` | true | false |
| Mutating | `create`, `start`, `stop`, `apply_recipes`, `update`, `clone`, `snapshot`, `restore`, `forward`, `wait` | false | false |
| Destructive | `destroy`, `prune` | false | true |
| Execution | `exec`, `copy_to`, `copy_from` | false | true, `openWorldHint` true |

Every schema sets `additionalProperties: false`, so an unexpected parameter is
rejected rather than silently ignored (OWASP MCP guidance).

Tool descriptions are the full text a user would see, and carry no hidden
instructions. That is the defence against description poisoning being
invisible in a client's abbreviated UI.

`update` must strip `share` from any patch built from agent input, per §7.1
#2, since `core.Patch` is exactly the generic-map shape that makes it reachable.

## 6. Testing

`test_guards.py` is the important one and should be adversarial: symlink
escapes, `..` traversal, absolute paths, a path whose prefix matches the
sandbox as a STRING but not as a directory (`~/.stoat/shared/work-evil`),
unicode tricks, empty and whitespace names.

`test_client.py` uses a fake `stoat` binary (a script echoing fixed JSON
Lines) rather than the real one, so envelope handling is testable without
VMs: a missing result line, an unknown event type, a nonzero exit with a valid
error envelope, invalid JSON mid-stream.

`test_server.py` asserts every tool declares `additionalProperties: false`,
that no forbidden surface from §4 is registered, and that annotations match
the table in §5.

## 7. Out of scope for v1

Stated so nobody builds them by accident: HTTP transport (stdio only),
authentication, multi-user, and any tool that writes to the host outside
`~/.stoat/shared/<vm>/`.
