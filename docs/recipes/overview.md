# Recipes

A recipe is a named directory with a `recipe.toml` manifest and one or more
scripts. Stoat selects recipes by the guest OS and the capabilities declared by
that guest. The same recipe can run over SSH on an already booted VM or from a
cloud-init seed at first boot.

## Recipe directories

User recipes live under `~/.stoat/recipes/`, or under `$STOAT_HOME/recipes/`
when `STOAT_HOME` is set. A recipe directory must contain `recipe.toml` and
the script named by its `script` field. The manifest's `[scripts]` table can
select another script for a particular OS or one of that OS's aliases.

Stoat installs bundled recipes into this directory. It records checksums in
`.manifest`; an unchanged bundled file can be refreshed by an upgrade, while a
hand-edited file is preserved. Remote recipes use a project cache at
`.stoat/recipes/` or a global cache under `~/.stoat/recipes/`. See
[Sharing recipes](sharing.md).

## When recipes run

`stoat apply <vm>` runs the VM's selected recipes after the VM is running and
reachable over SSH. `--only` restricts the run to names already present in the
VM's recipe list. `stoat apply --dry-run` reports the run or skip decision for
each selected recipe without contacting the guest.

For a live or installed disk VM, the TUI waits for SSH after start and begins
an apply operation when the VM needs one. It checks a live VM on each start
because its root filesystem is temporary. A `run = "once"` recipe can still be
skipped from its host-side record after a live restart; see
[Troubleshooting](../troubleshooting.md#a-live-vm-lost-everything-after-a-reboot).
Cloud-init recipes run from the seed at first boot. Stoat discovers their
marker files if a later `apply` needs to reconcile state.

## Targeting and execution

The manifest uses `os` to restrict a recipe to named guests and `requires` to
require guest capabilities. An empty `os` applies to every loaded guest.
Stoat must satisfy every entry in `requires`. The guest's `init` value is also
available as a capability. `stoat guest show <name>` displays the values.

The default `stage` is `provision`. An `install` stage is accepted in the
manifest but is not executable; `stoat check-recipes` reports
`install-stage recipes are not yet supported`. Alpine disk installation is
handled by Stoat's unattended installer instead.

The default `runtime` is `sh`, invoked as `sh -s`. `runtime = "python3"` is
invoked as `python3 -`; Stoat installs `python3` with the guest package
manager first when the guest does not provide it. The only accepted runtimes
are `sh` and `python3`.

`depends` names recipes that must run first. The dependency must be in the VM's
recipe list, unless it was already applied. Stoat orders the run and rejects
cycles or unsatisfied dependencies. The TUI can add missing dependencies when
you select a recipe; the CLI requires you to include them in the recipe list.

`run` defaults to `once`. `once` skips a recipe whose current script and
resolved non-secret parameters match its applied record. A changed script or
parameter runs again. `always` runs on every apply. `manual` runs only when its
name appears in `stoat apply <vm> --only <name>`. The `auto` manifest field is
stored and shown by the manifest parser but does not control the TUI's
auto-provision decision; the TUI decides from VM mode and applied state.

Set `reboot = true` when a recipe needs a disk VM to restart before its effect
is visible. Stoat performs one reboot after all recipes in the apply run have
succeeded. It does not reboot live VMs because a live root is temporary.

## Bundled recipes

The bundled set is closed and currently contains these four recipes:

| Recipe | Supported guests | Purpose |
|---|---|---|
| `devtools` | Alpine, Ubuntu, Debian, Fedora, Arch | Git, compiler tools, editor and basic fetch tools |
| `docker` | Alpine, Ubuntu, Debian, Fedora, Arch | Docker engine and compose plugin; schema 3 parameter `user`, output `socket`, health check `docker info` |
| `tailscale` | Alpine, Ubuntu, Debian, Fedora, Arch | Install and start `tailscaled`; schema 3 required secret `authkey`, health check `tailscale version` |
| `xfce` | Alpine, Ubuntu, Debian, Arch | XFCE desktop with autologin startx on tty1; requests a disk-VM reboot |

Each bundled recipe has a manifest. Docker, devtools, and Tailscale use
OS-specific script overrides because package names and repository setup
differ. XFCE uses one script with guest prelude verbs and therefore has no
`[scripts]` table.

Cloud images use their own package manager and a cloud-init seed. Cloud-init
currently wraps recipe bodies in shell commands; it does not perform the SSH
runtime bootstrap. Use `runtime = "sh"` for a cloud recipe. A `python3`
manifest is not installed or invoked by cloud-init. A shared package name must
resolve on every guest listed in `os`; use an OS-specific script when it does
not.

## Recipe state

After a successful apply, Stoat records the recipe version, script and input
hashes, outputs, health result and timestamp in the VM's `[applied]` table.
Secret values never enter that table or JSON output. A secret is represented as
`<set>` or `<unset>` when Stoat displays state.

Run a declared health check after applying a recipe with `stoat wait <vm>
--healthy`. A check exits with status 0 for healthy. If `health.timeout` is
omitted, Stoat uses 30 seconds. A recipe with no `[health]` table has no check.

Continue with [Writing your own recipe](writing-your-own.md) or
[Sharing recipes](sharing.md).
