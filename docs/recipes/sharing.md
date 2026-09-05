# Sharing recipes

Remote recipes come from a Git repository with a `recipe.toml` at its root.
Stoat validates the manifest, pins the resolved commit in `stoat.lock`, and
keeps the checkout in `.stoat/recipes/` for a project or `~/.stoat/recipes/`
for the global scope. Git must be installed on the host.

## Add and search

Search the configured index by name or description:

```sh
stoat recipe search my-tools
```

Add an index entry by name. An index name does not prompt for confirmation:

```sh
stoat recipe add my-tools
```

Add directly from a repository URL when the source is not in the index. A TTY
shows the manifest name, target OSes, requirements, and parameters before it
asks for confirmation. `-y` skips that prompt:

```sh
stoat recipe add https://github.com/example/stoat-my-tools@main -y
```

The default index is configured as
`https://github.com/novusedge/stoat-recipes`, but that repository currently
returns 404 and is not operational. Set `STOAT_INDEX` to a reachable Git
repository containing `index.toml` until a published index is available:

```sh
export STOAT_INDEX=/path/to/stoat-recipes
stoat recipe search my-tools
```

Stoat does not create or publish that repository. Index refreshes are cached
for 24 hours; `--refresh` forces a new fetch.

## Project and global scopes

If the current directory contains `stoat.toml`, recipe commands use project
scope. The declaration lives in its `[recipes]` table:

```toml
[recipes]
my-tools = "v1.2"
other-tools = { source = "https://github.com/example/stoat-other-tools", ref = "main" }
```

Project scope writes `./stoat.lock` and caches checkouts under
`./.stoat/recipes/`. Stoat adds `.stoat/` to `.gitignore` when the directory
is a Git checkout. Commit both `stoat.toml` and `stoat.lock` so another
checkout can reproduce the same recipe commits.

Without `stoat.toml`, commands use the global lock at `~/.stoat/stoat.lock`
and cache at `~/.stoat/recipes/`. Pass `--global` to force global scope from a
project directory. Stoat does not search parent directories for a project
file.

## Lock, sync, update, and remove

Resolve every project declaration to a commit without changing the cache:

```sh
stoat recipe lock
```

Populate the cache from the lock, removing project cache entries no longer in
the lock:

```sh
stoat recipe sync
```

Fetch refs again and repin one recipe, or every remote recipe when no name is
given:

```sh
stoat recipe update my-tools
stoat recipe update
```

Remove a remote recipe after checking that no VM uses it:

```sh
stoat recipe rm my-tools -y
```

Use `--force` only when intentionally removing a recipe listed by a VM. A
recipe checkout with local changes is never overwritten by update or sync;
copy it to a local recipe first.

`apply` in project scope checks that declarations, lock entries, and cache
checkouts agree. A stale declaration reports a repair instruction to run
`stoat recipe lock`; a missing or changed cache is synchronized before apply.
