# MCP server

`stoat mcp` serves Stoat's Model Context Protocol server over stdio. MCP
clients launch it as a subprocess, normally with the working directory of the
project they should manage. The server shares the wire DTO package and
contract version with the JSON CLI. Tool schemas, payloads, and envelopes are
MCP-specific; see [JSON output](json.md).

## Install a client entry

Stoat can write the `stoat` entry for these clients:

```sh
stoat mcp install claude-code
stoat mcp install claude-desktop
stoat mcp install cursor
stoat mcp install vscode
```

The command preserves other entries in the client's JSON file. Use
`--print` to print the entry without writing a file. Use `--project` to write
the current directory's project entry where the client supports it:

```sh
stoat mcp install claude-code --project
stoat mcp install vscode --project
```

The installed entry includes the absolute Stoat executable, the `mcp`
argument, and the current working directory. The working directory determines
which `stoat.toml` the server reads. Claude Code and VS Code support a project
entry; Claude Desktop and Cursor use their user configuration only.

Check the contract, transport, executable, and client entries with:

```sh
stoat mcp doctor
```

`mcp doctor` marks an entry as stale when its command points to a different
Stoat executable. Re-run `mcp install` after changing the installation path.

## Transport

The default transport is stdio:

```sh
stoat mcp
# Equivalent explicit form:
stoat mcp serve
```

Streamable HTTP is available for a client that cannot launch a subprocess:

```sh
stoat mcp --http 127.0.0.1:7777
```

The HTTP address must be loopback (`127.0.0.1`, `::1`, or `localhost`). The
server has no authentication, so it refuses addresses that bind another
interface.

The default rate limits are 30 calls in the per-tool burst with a refill of
0.5 calls per second, and 60 calls in the shared burst with a refill of 2
calls per second. Change them with `--tool-burst`, `--tool-rate`, `--burst`,
and `--rate` when starting the server.

## VM access levels

`agent_access` in a VM's `vm.toml` controls guest access. Levels include all
permissions below them:

| Level | Guest operations |
|---|---|
| `none` | No guest operation. Host-side status, lifecycle, snapshots, logs, forwarding, recipe management and project operations remain available. |
| `observe` | `read_file`, `list_dir`, `stat`, `ps`, `svc_status`, and `tail_log`. |
| `manage` | Observe operations plus `write_file`, `copy_to`, `copy_from`, `pkg_install`, `svc`, `useradd`, and `apply_recipes`. |
| `exec` | Manage operations plus `exec`, `exec_bg`, `job_status`, `job_output`, `job_kill`, and `list_jobs`. |

`create` defaults to `manage`. The CLI and TUI can raise or lower a VM's
level. The MCP `update` tool can lower a level but refuses to raise it. The
level is checked by the MCP server; `stoat exec` and `stoat cp` do not enforce
it when called directly by a person.

## Tools

The server registers these tools. Most inspection tools return data without
changing the guest. `recipe_schema` may refresh bundled recipe files, and
`search_recipes` may refresh the local index clone; treat those host-side
caches as mutable. Other tools may change VM, recipe, or guest state; tools
that run guest code are marked by their required level.

### Host and recipe tools

The read-only capabilities tool reads host checks and stored VM metadata. Its
optional vm argument selects one VM; omit it for host and project scope. It
does not start or connect to a VM, and it reports MCP access limits plus
unavailable fork and continuation proposals. Discovery does not mutate a VM.

| Tool | Purpose |
|---|---|
| `list_vms`, `vm_status` | List VMs or inspect one VM, including recipe state and health. |
| `list_images` | List catalog and downloaded images. |
| `list_recipes`, `check_recipes`, `recipe_schema`, `search_recipes` | Inspect recipe availability, contract, or index entries. |
| `plan_recipes` | Show what `apply_recipes` would run or skip without running it. |
| `add_recipe`, `update_recipe`, `remove_recipe` | Add, repin, or remove remote recipes. `add_recipe` accepts curated index names only; it refuses Git URLs. `remove_recipe` has no force option. |
| `list_guests`, `guest_info` | Inspect loaded guest definitions and their package/service commands. |
| `doctor`, `logs` | Check host prerequisites or tail a VM's console/apply log. |
| `capabilities` | Read host checks and optional stored VM metadata; report current capabilities and limits. |
| `create`, `start`, `stop`, `destroy`, `update`, `clone` | Manage VM definitions and lifecycle. `destroy` deletes the VM and its disk. |
| `snapshot`, `restore`, `forward`, `wait`, `prune` | Manage disk snapshots, port forwards, state waits, and stale files. `prune` is dry-run unless `apply=true`. |
| `project_status`, `project_up`, `project_down`, `project_apply`, `project_wait` | Inspect or operate on every VM declared by the server working directory's `stoat.toml`, in declaration order. A failure stops the run and later VMs are marked skipped. |

`wait` accepts `reachable`, `applied`, or `stopped`; `healthy=true` waits for
the applied recipes' health checks. Its `timeout_seconds` is a count of
seconds capped at 600, not a duration string.

### Guest tools

| Tool | Required level | Purpose |
|---|---:|---|
| `apply_recipes` | `manage` | Run the VM's configured recipe scripts, optionally with a named subset. |
| `read_file`, `list_dir`, `stat`, `ps`, `svc_status`, `tail_log` | `observe` | Read guest files, directories, process state, service state, or logs. Guest paths are absolute. |
| `write_file`, `copy_to`, `copy_from`, `pkg_install`, `svc`, `useradd` | `manage` | Modify files, copy data under the VM's shared directory, install packages, manage services, or add a user. |
| `exec`, `exec_bg`, `job_kill` | `exec` | Run or signal arbitrary guest commands. `argv` is an argument array, not a shell string. |
| `job_status`, `job_output`, `list_jobs` | `exec` | Inspect background jobs created by `exec_bg`. A guest reboot clears the job files and a job can become `unknown`. |

Guest operations require a running VM. `copy_to` and `copy_from` restrict the
host side to the VM's configured shared directory. `pkg_install` uses the
loaded guest definition's package manager. `svc` uses its service templates.

## Project tools and scope

Project tools use the directory in which the server was started. They do not
accept a project path in a tool argument. Start the server from the project,
or install a project entry that records that working directory. If the server
has no `stoat.toml`, project tools fail with an instruction to run `stoat init`
or use a named VM tool.

Project recipe state follows the same rules as the CLI: `stoat.toml` declares
recipes, `stoat.lock` pins commits, and `.stoat/recipes/` holds checkouts. A
project operation repairs a missing or stale clean checkout from the lock and
refuses a stale declaration or dirty checkout.

## Output and limits

MCP tool results use the DTOs described in [JSON output](json.md). Errors are
returned with `isError: true`. The first content block is a text block with the
same human-readable message as the CLI. The server also adds the complete
`wire.ErrorInfo` under `_meta["io.github.novusedge.stoat/error"]` and appends a
second text block containing JSON in the form `{"error":{...}}`. Callers
should inspect `isError`, then branch on `error.code` from metadata or the
second text block. They must not branch on `structuredContent`: for typed
handler errors, the SDK owns that field and keeps the schema-valid typed
output. Receiving-middleware errors, such as rate-limit refusals, can leave
that field unset.

Successful results keep their existing DTO, `structuredContent`, content
fallback, and output schema. SDK argument validation and MCP protocol errors
are outside this tool-result contract. Binary file content is base64 encoded.
Read, directory, process, log, and command output sizes are capped by the
server; the tool schema states the cap for each input.

Recipe and guest tools read manifests and definitions on the host. They do not
run a recipe unless the caller selects `apply_recipes`, a project apply tool,
or a lifecycle operation whose documented behavior starts a VM and applies
its configured recipes.
