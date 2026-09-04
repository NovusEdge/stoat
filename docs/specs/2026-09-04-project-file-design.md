# Project file

Spec 5 of 5. Depends on specs 2 (params, secrets) and 3 (declarations,
lock, scope). Status: approved 2026-09-04.

## Goal

A repository declares its VMs in `stoat.toml`. `git clone` then `stoat up`
gives a contributor the same VMs, recipes, and params. The model is
`docker compose` for VMs and `uv` for pinning.

## The file

```toml
# stoat.toml
schema = 1

[project]
name = "myrepo"                # default: the directory name

[recipes]                      # spec 3
tailscale = "v1.2"

[vms.dev]
name    = "shared-dev"         # optional; default "<project>-dev"
image   = "ubuntu-24"
cpus    = 4
ram     = 4096
disk    = "20G"
recipes = ["docker", "tailscale"]
shares  = ["."]                # "." or a subdirectory; mounted under /work
agent_access = "manage"        # spec 4; default manage

[vms.dev.params.docker]        # spec 2
user = "dev"
```

Rules:

- A VM key matches the VM name grammar. Its global name is `name` if
  set, otherwise `<project>-<key>`. Two declarations resolving to one
  global name is an error.
- `image` is a catalog id or a BYO path relative to the project. `disk`
  applies to disk-mode images only.
- `shares` entries are `.` or a subdirectory of the project; anything else
  is an error. Each mounts at `/work/<basename>`, `.` at `/work`.
- Every field except `image` is optional and takes the same default as
  `stoat new`.
- `[vms.<key>.params.<recipe>]` holds non-secret params. Secrets live in
  `.stoat/secrets.toml` (0600, gitignored) as `<key>.<recipe>.<param>`.
- `tomlx` loads it with `Reject`.

## Scope

`stoat.toml` in the current directory activates project scope, the same
test spec 3 uses. No walk-up.

At project scope:

- A bare VM argument resolves to the declaration key first, then to a
  global name. `stoat ssh dev` reaches `myrepo-dev`.
- `up`, `down`, `apply`, `wait`, `status` with no VM argument act on every
  declared VM, in declaration order. `up` and `apply` respect recipe
  dependency order within a VM as today.
- `ls` shows every VM as today plus a `project` column, and `--project`
  filters to the current one.
- `new` refuses at project scope with `declare the VM in stoat.toml and run
  stoat up`, unless `--global`.

## Reconcile

`stoat up` for a declared VM:

1. Missing: create from the declaration, then start.
2. Present: compare the declaration to `vm.toml`. `cpus`, `ram`,
   `recipes`, `params`, `shares`, `agent_access` are applied through the
   same path as `stoat update`; a change that needs a restart is reported
   and applied at the next `down`/`up`. `image` and `disk` are immutable;
   a difference is an error naming `stoat rm dev` as the fix.
3. Start, then apply recipes as the auto-apply on boot does today.

`vm.toml` gains `project = "/abs/path/to/dir"`, written at create. A VM
whose `project` directory no longer exists still lists and runs; `ls`
shows the path with `(missing)`.

`stoat down` and `stoat rm` at project scope with no argument act on every
declared VM; `rm` asks per VM on a TTY and refuses without `-y` otherwise,
the same as today.

## Commands

| Command | Effect |
|---|---|
| `stoat init [--name n]` | writes `stoat.toml` from the annotated sample, with one VM, and appends `.stoat/` to `.gitignore` in a git checkout |
| `stoat up [vm…]` | reconcile and start |
| `stoat status` | one line per declared VM: global name, state, health, drift (`cpus 2 → 4`) |
| `stoat ls --project` | filter |

Existing commands gain the resolution rule and the no-argument default;
their flags do not change.

## MCP

- `list_vms` items gain `project` and `key`.
- `up`, `down`, `apply`, `wait` accept a missing `vm` at project scope and
  act on every declared VM; the result is a list keyed by global name.
- `project_status` mirrors `stoat status`. Read-only.
- The server's project scope is its cwd, set by `stoat mcp install`.

## Errors

| Condition | Message |
|---|---|
| duplicate global name | `stoat.toml: vms.dev and vms.ci both resolve to "myrepo-dev"` |
| share outside project | `stoat.toml: vms.dev.shares: "../secrets" is outside the project` |
| immutable change | `dev: image changed (ubuntu-24 → debian-12); run stoat rm dev and stoat up` |
| new at project scope | `a stoat.toml is present; declare the VM there and run stoat up, or pass --global` |
| unknown key in a bare argument | `no VM "db" in stoat.toml or ~/.stoat/vms` |

## Testing

- Declaration to `vm.toml` golden for a full and a minimal `[vms.x]`.
- Name resolution: key, `name` override, global fallback, duplicate.
- Reconcile: each mutable field changes `vm.toml`; `image` and `disk`
  error; restart-needed fields reported.
- Shares: `.`, subdirectory, escape refused.
- Secrets: `.stoat/secrets.toml` read at apply, redacted in `status --json`.
- `init` output decodes in `Reject` mode and matches the sample.
- No-argument commands act in declaration order; `--global` bypasses.
- MCP: `list_vms` fields; `up` with no `vm` at project scope.
