# Project workflow

Use a project file when a repository needs the same VM definitions for every
checkout. `stoat.toml` is committed. The `.stoat/` cache and its secrets are
ignored. The project lock file is committed with the declaration.

## Create the declaration

From the repository root, run:

```sh
stoat init
```

This writes `stoat.toml` with one example VM. It refuses to overwrite an
existing file. In a Git checkout it also adds `.stoat/` to `.gitignore`.
Edit the image, VM key, resources, recipes, and shares. `image` is required;
the other fields use the same defaults as `stoat create` when omitted.

For example:

```toml
schema = 1

[project]
name = "myrepo"

[vms.dev]
image = "ubuntu-24.04"
cpus = 4
ram = 4096
disk = "20G"
recipes = ["docker"]
shares = ["."]
agent_access = "manage"

[vms.dev.params.docker]
user = "dev"
```

The key `dev` resolves to `myrepo-dev` unless `name` overrides it. A command
typed in the project directory resolves a bare key first, then a declared
global name. Scope is current-directory only; stoat does not search parent
directories. See [The project file](../reference/project-file.md) for all
fields and validation rules.

## Pin and cache recipes

Declare a recipe in `[recipes]`, then resolve it to a commit:

```sh
stoat recipe add https://github.com/OWNER/REPOSITORY.git@TAG
stoat recipe lock
stoat recipe sync
```

Replace the URL and tag with a repository that contains a valid `recipe.toml`.
The repository's current `index.toml` has no published entries, so an index
name cannot be resolved from this checkout. Bundled recipes such as `docker`
are already available and do not need `recipe add`.

An index name can be used when the configured index contains it. A Git URL can
be passed instead, with an optional `@tag` or branch. `recipe add` writes the
project declaration, lock entry, and cache entry as one operation. Run
`recipe lock` after editing `[recipes]`; it updates `stoat.lock` but does not
populate the cache. Run `recipe sync` to make `.stoat/recipes/` match the lock.

The project lock is `./stoat.lock`. A command outside project scope uses the
global lock at `~/.stoat/stoat.lock` and the global cache at
`~/.stoat/recipes/`; pass `--global` to force that scope from a project
directory. Do not edit a lock entry by hand. Commit `stoat.lock` so another
checkout runs the same recipe commits.

## Keep secrets out of Git

Non-secret recipe parameters belong in `[vms.<key>.params.<recipe>]`. Put
secret parameters in `.stoat/secrets.toml`, which stoat writes with mode
`0600` and never includes in status output. Its keys use the declaration key:

```toml
[dev.tailscale]
authkey = "tskey-..."
```

Do not put the secret in `stoat.toml`, `stoat.lock`, or a command line. A
missing required secret fails when stoat validates the recipe before it runs.

## Start and reconcile

Check the declaration and existing VM state without changing anything:

```sh
stoat status
```

Create missing VMs, reconcile mutable fields, and start every declaration in
file order:

```sh
stoat up
```

`up` creates a missing VM from its declaration. For an existing VM it applies
CPU, memory, recipe, parameter, share, and agent-access changes through the
same validation as `stoat update`. Image and disk changes are immutable; stop,
remove, and recreate that VM when those fields change. CPU, memory, and shares
are saved immediately but take effect at the next down and up.

`up` waits for Alpine disk auto-installation to finish. It then waits for SSH
and applies pending recipes. Use `--no-apply` when the VM should start without
the post-boot recipe pass. A named invocation operates on one declaration:

```sh
stoat up dev
stoat wait dev --healthy
```

For a cloud VM, changing the recipe list changes the declaration used by later
reconciliation, but it does not rebuild the first-boot seed. Recreate the VM
when the new list must be present in cloud-init. Existing recipe scripts can
still run through the normal apply policy after SSH is ready.

The no-argument project commands `up`, `down`, `apply`, `wait`, and `rm` stop
at the first failure and report later declarations as skipped. `rm` requires
`-y` when used without an interactive confirmation.

## Inspect drift and resolve failures

`stoat status` reports each declaration's global name, state, health, and
drift. A missing VM is reported as `missing`. An image or disk change reports
the remove-and-recreate command. A share outside the repository, including a
symlink that resolves outside it, is rejected before a VM is changed.

Use the [troubleshooting guide](../troubleshooting.md) for readiness, apply,
recipe lock, and project-scope errors.
