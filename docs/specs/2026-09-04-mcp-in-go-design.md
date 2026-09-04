# MCP server in the Go binary

Spec 4 of 5. Depends on specs 1 to 3 for the new tools. Status: approved
2026-09-04.

## Goal

`stoat mcp` serves the MCP protocol from the stoat binary. The Python
package under `mcp/` is deleted once the Go server passes the same guard
and annotation tests.

## Why move

- `github.com/modelcontextprotocol/go-sdk` is v1.7 with an API
  compatibility guarantee, tool annotations, JSON schema generation from
  structs, stdio and streamable-HTTP transports, and middleware.
- fastmcp has shipped two breaking majors (v3, v4) since `mcp/pyproject.toml`
  pinned `fastmcp>=2.0`.
- Every MCP client launches a stdio server as a subprocess. A static binary
  needs no interpreter, venv, or `uv`.

## Layout

```
internal/mcpsrv/
  server.go        registers tools, builds the server, picks the transport
  tools_*.go       one file per tool class: read, vm, recipe, exec, guest
  guards.go        name, path, image id, index name, param name, flag-free
  ratelimit.go     per-tool and shared token buckets, as middleware
  redact.go        secret redaction over wire values
  contract.go      contract version, shared with internal/cli/wire
cmd: `stoat mcp [--http addr]`, `stoat mcp install <client>`, `stoat mcp doctor`
```

`mcpsrv` imports `core` and `wire`. It does not shell out. Tool outputs are
the `wire` DTOs, so the `--json` contract and the MCP schema are one set
of types. `TestJSONEnvelopeEveryCommand` keeps guarding the CLI side; a
sibling test in `mcpsrv` guards that every tool's output type is a `wire`
type.

## Transport

- stdio by default.
- `--http 127.0.0.1:PORT` serves streamable HTTP for a client that needs
  it. Loopback only; a non-loopback address is refused. No auth in this
  spec, since the bind is loopback.

## Tools

Existing tools keep their names, parameters, and annotations. The table in
`docs/design/mcp-server.md` §5 moves to `internal/mcpsrv/tools_test.go` as
the source of truth and the doc links there.

Additions from specs 1 to 3:

| Tool | Class | Backing call |
|---|---|---|
| `list_guests` | read-only | `core.Guests()` |
| `guest_info(name)` | read-only | `core.Guest(name)` |
| `recipe_schema(name)` | read-only | `core.RecipeSchema(name)` |
| `update(vm, patch?, params?, secrets?)` | mutating | `core.Update` |
| `wait(vm, healthy?)` | mutating | `core.Wait` |
| `search_recipes(term)` | read-only | `core.SearchRecipes` |
| `add_recipe(name, ref?)` | mutating | `core.AddRecipe`; index names only |
| `update_recipe(name?)` | mutating | `core.UpdateRecipe` |
| `remove_recipe(name)` | mutating | `core.RemoveRecipe`; no `force` |

`vm_status` gains `recipes[]` through `wire.VMStatus`.

Every input struct carries `jsonschema` tags and the generated schema sets
`additionalProperties: false`. A test asserts it for every tool.

## Guards

Ported one to one from `mcp/stoat_mcp/guards.py`, as functions called at
the top of each handler:

- `checkVMName`, `checkHostPath`, `checkImageID`, `checkFlagFree`,
  `stripForbidden` (patch keys `share`, `image`, `base`, `iso`,
  `console_password`).
- New: `checkIndexName` (index name grammar, no `/`, `:`, `@` beyond one
  `@ref`, no traversal), `checkParamName` (spec 2's regex).

`requireExecAllowed` stays in the server, since `core.Exec` does not
enforce it. It gates `exec`, `copy_to`, `copy_from`, `apply_recipes`.

Rate limiting is receiving middleware: a per-tool bucket (30, 0.5/s) and a
shared bucket (60, 2/s), both checked before either is charged. The
numbers move to flags on `stoat mcp` with those defaults.

Redaction is sending middleware over `wire` values: any field whose
manifest type is `secret` renders as `<set>` or `<unset>`. The `secrets`
input of `update` is never logged, never echoed, and is dropped from the
request before any log line is written.

Clamps: `wait` at 600 s, `logs` at 2000 lines, unchanged.

## Contract

`wire.Contract` becomes 3. `stoat mcp` and `stoat --json version` read
the same constant, so the runtime check the Python server did against a
separate process is now a compile-time fact. `stoat mcp doctor` reports
the contract, the transport, and whether the client entry from `install`
points at the running binary.

## Install

`stoat mcp install <client>` writes the client's config entry:

| client | file |
|---|---|
| `claude-code` | `~/.claude.json` `mcpServers.stoat` or `.mcp.json` in cwd with `--project` |
| `claude-desktop` | `~/.config/Claude/claude_desktop_config.json` |
| `cursor` | `~/.cursor/mcp.json` |
| `vscode` | `.vscode/mcp.json` in cwd |

The entry is `{ "command": "<abs path to stoat>", "args": ["mcp"], "cwd": "<cwd>" }`.
`cwd` is written so project scope (spec 3) applies to the server. An
existing `stoat` entry is replaced; other entries are untouched; the file
is written atomically. `--print` shows the JSON instead of writing.

## Project scope

With `stoat.toml` in the server's cwd, every tool runs at project scope,
the same as the CLI. `list_vms` gains a `scope` field per VM once spec 5
defines project VMs; until then it reports `global`.

## Deletion

`mcp/` is deleted in the same change, after:

- every test in `mcp/tests/test_guards.py` and `test_server.py` has a Go
  counterpart in `internal/mcpsrv`, tracked in a table in the plan;
- `docs/design/mcp-server.md` is rewritten for the Go server, and
  `docs/reference/json.md` notes that the MCP schema is the `wire` types.

## Errors

Tool errors return `IsError: true` with the message from `wire.MapError`,
the same text the CLI prints. A guard failure names the guard:
`invalid vm name "a/b": must match ^[a-z0-9][a-z0-9-]*$`.

## Testing

- Annotation table: every tool's `readOnlyHint`, `destructiveHint`,
  `openWorldHint` against the class table.
- Schema: `additionalProperties: false` on every input; no `share`,
  `image`, `base`, `iso`, `console_password` in any input; no em dash in
  any description.
- Guards: the Python table ported case for case.
- Rate limit: per-tool burst refused at 31; shared burst refused at 61
  across tools; neither bucket charged on refusal.
- Redaction: a fixture VM with a sentinel secret; every tool's output is
  scanned for it.
- `requireExecAllowed` refuses the four tools on `allow_exec = false`.
- Install: each client's config written into a temp home; existing
  unrelated entries preserved.
- In-process client from the SDK drives the server over stdio for an
  end-to-end `list_vms` and `plan_recipes` round trip.
