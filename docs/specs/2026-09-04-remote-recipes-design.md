# Remote recipes

Spec 3 of 5. Depends on the recipe contract v3 spec (`tomlx`, schema field).
Status: approved 2026-09-04.

## Goal

Install a recipe from a git repository by name or URL, pin the commit in a
lockfile, update it on request. Today a recipe exists only if it is bundled
or copied by hand into `~/.stoat/recipes/`.

## Scope

In: `recipe add`, `recipe update`, `recipe rm`, `recipe search`, a curated
index, a lockfile at two scopes, the MCP contract for these.

Out: the project file itself (`stoat.toml`: VMs, params, `stoat up`). Spec 5
defines it. This spec defines only where the lock and the recipe cache
live when `stoat.toml` is present in the current directory.

## Sources

A recipe repository is the recipe directory: `recipe.toml` at the root,
scripts beside it, one recipe per repository. That keeps a lock entry, a
directory, and a name one-to-one.

`stoat recipe add <ref>` accepts:

- an index name: `tailscale`, `tailscale@v1.2`
- a URL: `https://github.com/x/stoat-tailscale@v1.2`,
  `git@github.com:x/stoat-tailscale.git@main`

`@ref` is a tag or branch. It defaults to the repository's default branch.

## Index

A git repository, default `https://github.com/novusedge/stoat-recipes`,
overridden by `STOAT_INDEX`. It holds one file:

```toml
# index.toml
schema = 1

[recipes.tailscale]
source      = "https://github.com/x/stoat-tailscale"
description = "join a tailnet on boot"
os          = ["alpine", "debian", "ubuntu", "fedora", "arch"]
```

The index clones to `~/.stoat/index/`. `recipe add <name>` and
`recipe search` refresh it when its last fetch is older than 24 hours or
when `--refresh` is passed. The index carries names and URLs only. The
pinned commit is the user's lock, so an index change cannot move a VM's
recipe.

## Lockfile

```toml
# stoat.lock: written by stoat; do not edit
schema = 1

[recipes.tailscale]
source = "https://github.com/x/stoat-tailscale"
ref    = "v1.2"
commit = "9f3c1e2a7b…"        # full sha
added  = "2026-09-04T10:00:00Z"
```

Scope resolution:

- `stoat.toml` in the current directory: the lock is `./stoat.lock`, the
  cache is `./.stoat/recipes/<name>/`. `recipe add` appends
  `.stoat/` to `.gitignore` if the directory is a git checkout and the
  line is absent.
- Otherwise: `~/.stoat/stoat.lock` and `~/.stoat/recipes/<name>/`.
- `--global` forces the home scope.
- No walk-up. Only the current directory is checked.

A project entry shadows a global entry of the same name. `recipe ls`
gains a `scope` column: `bundled`, `local`, `global`, `project`. A lock
with a newer `schema` is an error naming the running stoat version.

A local recipe directory (copied by hand, or from `recipe new`) has no lock
entry and is left alone.

## Declaration, lock, cache

The model is `uv`'s: `pyproject.toml` declares, `uv.lock` pins, `.venv`
is the cache. Here:

| | declares | pins | cache | committed |
|---|---|---|---|---|
| project | `stoat.toml` `[recipes]` | `stoat.lock` | `.stoat/recipes/` | toml and lock |
| global | none | `~/.stoat/stoat.lock` | `~/.stoat/recipes/` | no |

Project declaration, the only part of `stoat.toml` this spec defines:

```toml
# stoat.toml
[recipes]
tailscale = "v1.2"                                   # index name, ref
xfce      = { source = "https://github.com/x/stoat-xfce", ref = "main" }
```

A ref is a tag, a branch, or a commit. There is no range syntax; git has no
semver resolution and a fake one would mislead. `recipe lock` resolves a
branch to the commit at its head at that moment.

Verbs, with the `uv` verb each mirrors:

| Command | `uv` | Effect |
|---|---|---|
| `recipe add <ref>` | `uv add` | writes the declaration (project scope), resolves, writes the lock, clones |
| `recipe lock` | `uv lock` | resolves every declaration to a commit, rewrites the lock; touches no cache |
| `recipe sync` | `uv sync` | clones or checks out every lock entry into the cache; removes cache entries absent from the lock |
| `recipe update [name]` | `uv lock --upgrade` | re-resolves the ref, rewrites the lock, syncs |
| `recipe rm <name>` | `uv remove` | removes the declaration and the lock entry, syncs |

`apply`, `up`, and `plan_recipes` in project scope run `recipe sync`
first when the lock is newer than the cache, so a fresh checkout works
after `git clone` plus `stoat up`. A lock entry without a declaration is
removed on the next `recipe lock`; a declaration without a lock entry
makes `apply` fail with `stoat.lock is out of date; run stoat recipe lock`.

Global scope has no declaration file. `recipe add --global` writes the
lock directly, and `recipe sync --global` rebuilds `~/.stoat/recipes/` from
it.

## Commands

| Command | Effect |
|---|---|
| `recipe add <ref> [-y] [--global]` | declare, resolve, clone at `ref`, validate, write the lock entry |
| `recipe lock [--global]` | resolve declarations to commits, rewrite the lock |
| `recipe sync [--global]` | make the cache match the lock |
| `recipe update [name] [--global]` | fetch `ref` again, check out, rewrite `commit`; all remote recipes when no name |
| `recipe rm <name> [--global]` | remove the declaration, the lock entry, and the directory; refuse if a VM lists it unless `--force` |
| `recipe search <term>` | match name and description in the index |
| `recipe ls` | as today, plus `scope` and `commit` (short) |

Validation on `add` and `update`:

- `recipe.toml` present at the root and loads with `tomlx` `Reject`.
- No symlink in the tree points outside it.
- The name in `recipe.toml` equals the lock name.
- The name collides with no bundled, local, or same-scope remote recipe.

`add` from a URL on a TTY prints `name`, `os`, `requires`, and params, and
asks for confirmation. `-y` skips it. Index names skip the prompt.

`update` refuses a directory with uncommitted changes and tells the user
to copy it to a local recipe first.

`git` is required for these commands. `doctor` reports it as optional,
with the install command per distro.

## Interaction with apply

`recipes.Dir()` becomes a list of roots in shadow order: project cache,
global cache, local, bundled. `Manifests()` walks all of them and applies
the shadow rule per name. The apply hash (spec 2) already covers the
script body, so a `recipe update` that changes the script re-applies a
`run = "once"` recipe and `plan_recipes` reports `run (script changed)`.

## MCP

- `search_recipes(term)`: read-only.
- `add_recipe(name, ref?)`: index names only; a URL is rejected by
  `check_index_name`. Mutating.
- `update_recipe(name?)`, `remove_recipe(name)`: mutating. `remove_recipe`
  has no `force`.

`add` runs no guest code. `apply` does, and it keeps the `allow_exec` gate.

## Errors

| Condition | Message |
|---|---|
| git missing | `git is required for recipe add; install it: <cmd>` |
| unknown index name | `no recipe "tailscal" in the index; run stoat recipe search tailscal` |
| ref not found | `x/stoat-tailscale: no tag or branch "v9"` |
| name collision | `"docker" is a bundled recipe; pick another name or use --force` |
| no manifest | `x/repo: no recipe.toml at the repository root` |
| dirty on update | `tailscale: local changes; copy it to a local recipe first` |
| newer lock | `stoat.lock: schema 2 is newer than this stoat (1)` |

## Testing

- Sources: a bare git repository under the test's temp dir. Index: a local
  directory passed through `STOAT_INDEX`. No network.
- `add` by name and by URL, `@tag` and default branch, lock entry golden.
- `update` moves `commit`; `update` refuses a dirty tree.
- Shadow order: project over global over local over bundled.
- Scope: `stoat.toml` present uses `./stoat.lock`; `--global` overrides.
- `lock` from a declaration-only `stoat.toml` produces the golden lock;
  `sync` from that lock into an empty `.stoat/` yields the tree; `sync`
  removes a cache entry absent from the lock; `apply` with a stale lock
  fails with the out-of-date message.
- Validation: symlink escape, missing manifest, name collision.
- `rm` refuses while a VM lists the recipe.
- MCP: `add_recipe` with a URL is refused; `check_index_name` covers
  traversal.
