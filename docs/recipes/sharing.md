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

The default index is `index.toml` at the root of the stoat repository,
`https://github.com/NovusEdge/stoat`, fetched as a shallow clone. To add a
recipe to it, open a pull request that adds an entry under `[recipes]`. Set
`STOAT_INDEX` to any Git repository or local directory that holds an
`index.toml` to use your own:

```sh
export STOAT_INDEX=/path/to/my-index
stoat recipe search my-tools
```

Index refreshes are cached for 24 hours; `--refresh` forces a new fetch.

List installed recipes and their scope and short commit pin:

```sh
stoat recipe list
```

Search reads the configured index. `update` addresses an existing remote pin
by its plain name and does not search the index again. This also applies to a
recipe added from a URL:

```sh
stoat recipe update my-tools
```

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

If an add would replace a bundled, local, or same-scope recipe, it refuses
unless the replacement is intentional:

```sh
stoat recipe add my-tools --global --force
```

For removal, use `--force` only when intentionally removing a recipe listed by
a VM. A recipe checkout with local changes is never overwritten by update or
sync; copy it to a local recipe first.

`apply` in project scope checks that declarations, lock entries, and cache
checkouts agree. A stale declaration reports a repair instruction to run
`stoat recipe lock`; a missing or changed cache is synchronized before apply.
