# stoat-mcp

An MCP server that exposes local QEMU VMs managed by `stoat` to an agent.

It is a thin wrapper: every tool validates its inputs, runs the `stoat`
binary with `--json`, and returns the resulting `data` object. No business
logic lives here; if a tool needed logic `stoat` itself does not have, that
would mean the layering is wrong, and the fix belongs in `stoat`'s Go core,
not in this package. See `docs/design/mcp-server.md` in the main repo for the
full design rationale, and `docs/reference/json.md` for the wire contract
this package decodes.

## What this is not

No HTTP transport (stdio only), no authentication, no multi-user support,
and no tool that writes to the host outside `~/.stoat/shared/<vm>/`. Some
CLI surfaces are deliberately never exposed as tools at all: a `share`
parameter on any mutating call, bring-your-own image paths, `recipe new`,
`ssh-command`, and the global (no VM name) `logs`. Their absence is the
control, not a runtime check.

## Setup

```
cd mcp
uv venv
uv pip install -e '.[dev]'
```

## Configuration

- `STOAT_BIN`: path to the `stoat` binary. Falls back to `PATH`.
- `STOAT_HOME`: the stoat data root. Falls back to `~/.stoat`.

## Running

```
.venv/bin/stoat-mcp
```

Serves over stdio. Refuses to start if the `stoat` binary cannot be found,
or if its JSON contract version does not match what this server was written
for.

## Testing

```
cd mcp
.venv/bin/pytest -q
```
