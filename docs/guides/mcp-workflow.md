# MCP workflow

The Go binary serves the Model Context Protocol over stdio by default. An MCP
client launches `stoat mcp` as a subprocess, so the client needs only the
installed binary. The server uses the process working directory as project
scope.

## Install a client entry

From the project directory when the client should use that project's
`stoat.toml`, run one of:

```sh
stoat mcp install claude-code
stoat mcp install claude-desktop
stoat mcp install cursor
stoat mcp install vscode
```

`claude-code`, `claude-desktop`, and `cursor` use their normal user config
files. VS Code uses `.vscode/mcp.json` in the current directory. Use
`--project` with Claude Code to write `.mcp.json` in the current directory.
`--print` prints the JSON entry without writing a file. An existing `stoat`
entry is replaced; other client entries remain.

Check the installed entry and server contract with:

```sh
stoat mcp doctor
```

The report includes contract version, transport, and client-entry status. The
server shares a contract version and DTO package with the JSON CLI.
Tool schemas and results are MCP-specific; see
[JSON output](../reference/json.md) for the differences.

## Choose a transport

Stdio is the normal transport:

```sh
stoat mcp
```

For a client that cannot launch a subprocess, serve streamable HTTP on a
loopback address:

```sh
stoat mcp serve --http 127.0.0.1:7777
```

The HTTP server rejects non-loopback addresses and has no authentication. Keep
it on loopback and use an authenticated tunnel if another machine must reach
it. The default per-tool limit is a burst of 30 calls with a refill of 0.5
calls per second. The shared server limit is a burst of 60 with a refill of 2
calls per second. Override them on `mcp serve` when the client needs another
bound.

## Set the VM access level

Set `agent_access` when creating a VM or in its project declaration:

```sh
stoat create --image ubuntu-24.04 --agent-access observe lab
```

The levels are cumulative. `none` allows host-side VM operations. `observe`
adds guest reads, process listings, service status, and log reads. `manage`
adds file writes, file copies, package installation, service changes, user
creation, and recipe application. `exec` adds foreground and background
commands plus job management. New VMs default to `manage`.

An MCP `update` can lower a VM's level but cannot raise it. Raise a level with
the CLI or TUI so a person grants that capability explicitly. The older
`allow_exec` field is still read for existing files. An explicitly stored
`true` maps to `exec`. A stored `false` value or an absent key maps to
`manage`.

## Use project tools

The server registers project tools in every process. They work when its fixed
working directory contains a `stoat.toml`; outside a project they return an
error explaining that scope. The project tools are:

| Tool | Effect |
|---|---|
| `project_status` | Read every declaration's state, health, and drift |
| `project_up` | Reconcile and start every declared VM in order |
| `project_down` | Stop every declared VM in order |
| `project_apply` | Apply every declared VM's recipes in order |
| `project_wait` | Wait for every declared VM to answer on SSH in order |

These tools use the server's fixed working directory. They stop at the first
failure and mark later entries as skipped. Without a project file, use
`start`, `stop`, `apply_recipes`, and `wait` with a VM name. The server does
not walk up to find a parent `stoat.toml`.

## Plan before applying

Use `plan_recipes` before `apply_recipes` to see which recipes will run, which
will be skipped, and why. `apply_recipes` requires `agent_access = manage` or
`exec`, runs the recipe scripts over SSH, and writes the same apply state and
log that the CLI uses. `wait` can wait for `reachable`, `applied`, or
`stopped`; `healthy` waits for SSH and every applied recipe's health check.

Read-only tools include VM and image listings, recipe applicability and
schemas, guest definitions, logs, and host checks. Guest read tools require
`observe`; guest writes, package, service, and user tools require `manage`;
command and job tools require `exec`. The server validates absolute guest
paths, caps large reads and listings, redacts secret values, and passes command
arguments without joining them into a shell string.
