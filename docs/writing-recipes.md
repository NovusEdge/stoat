# Writing recipes

A recipe is a directory with a `recipe.toml` manifest and one or more shell
scripts. Stoat runs the right script over ssh (or bakes it into cloud-init)
depending on the guest and backend. This is the v2 format; see
`docs/recipe-spec-v2.md` for the full spec this guide summarizes.

## Directory structure

```
~/.stoat/recipes/
  xfce/
    recipe.toml
    install.sh
    install-alpine.sh
    install-debian.sh
    install-arch.sh
  docker/
    recipe.toml
    install.sh
```

One directory per recipe, named after the recipe. `recipe.toml` is required;
everything else is shell scripts referenced from it.

## The manifest

`internal/recipes/bundled/docker/recipe.toml`, a minimal single-OS recipe:

```toml
name = "docker"
description = "Docker engine and the compose plugin"
os = ["alpine"]
requires = ["apk", "openrc"]
stage = "provision"
script = "install.sh"
```

`internal/recipes/bundled/xfce/recipe.toml`, with per-OS overrides:

```toml
name = "xfce"
description = "XFCE desktop with autologin startx on tty1"
os = ["alpine", "ubuntu", "debian", "arch"]
stage = "provision"
script = "install.sh"

[scripts]
alpine = "install-alpine.sh"
debian = "install-debian.sh"
arch = "install-arch.sh"
```

### Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | string | yes | | Identifier, matches the directory name |
| `description` | string | no | | One-line human description |
| `version` | string | no | | Semver; tracked per-VM alongside applied state |
| `os` | string[] | no | all OSes | Guest OSes this recipe applies to |
| `requires` | string[] | no | none | Capabilities the guest must have (below) |
| `stage` | string | no | `"provision"` | `"install"` or `"provision"` |
| `script` | string | yes | | Default script, path relative to the recipe dir |
| `scripts` | table | no | | Per-OS script overrides; unlisted OSes fall back to `script` |
| `run` | string | no | `"once"` | `"once"`, `"always"`, or `"manual"` |
| `auto` | bool | no | `false` | Run automatically the first time the VM becomes reachable |

`name` and `script` are the only required fields; a manifest missing either
fails to parse (`internal/recipes/manifest.go`).

## Capabilities

`requires` names facts about the guest rather than an OS list, so one
capability can cover several OSes at once and a recipe naturally excludes
guests that can't satisfy it:

| Capability | Meaning | OSes |
|------------|---------|------|
| `systemd` | systemd init | ubuntu, debian, arch, fedora |
| `openrc` | OpenRC init | alpine |
| `apt` | apt package manager | ubuntu, debian |
| `apk` | apk package manager | alpine |
| `dnf` | dnf package manager | fedora |
| `pacman` | pacman package manager | arch |

`docker/recipe.toml` uses `requires = ["apk", "openrc"]` instead of
`os = ["alpine"]` alone. Both narrow the recipe to Alpine today, but
`requires` also documents *why*: if another OpenRC/apk distro shows up later,
the recipe already applies to it with no manifest change. A recipe with both
`os` and `requires` must satisfy both.

A recipe requiring `systemd` is never offered to an Alpine VM; stoat resolves
this against `guest.OS` at list/check time, before ssh is even involved.

## Stages

### `provision` (default)

Runs after the VM has booted and is reachable over ssh. This is the common
case: install packages, enable services, write config.

- **apkovl/ssh backends**: the script is piped into `sh -s` over ssh, same as
  the current bundled recipes.
- **cloudinit backend**: the script is wrapped into the cloud-config
  `runcmd` block at VM creation and runs at first boot.

### `install`

Runs during initial disk setup, before the first real boot. On Alpine disk
mode this drives `setup-alpine` itself. Only one `install`-stage recipe makes
sense per VM; stoat errors at creation time if more than one is selected.

## Run modes

Set via `run` in the manifest, and enforced against the VM's `[applied]`
table in `vm.toml`:

- `once` (default): skip if the recipe is already recorded as applied.
- `always`: re-run on every `stoat apply`.
- `manual`: never runs automatically; only via an explicit
  `stoat apply <vm> --only <recipe>`.

`auto = true` is a separate toggle: it makes the recipe run automatically the
first time the VM becomes reachable, without waiting for an explicit
`stoat apply`. Default is `false`.

## OS-specific script overrides

`script` is the fallback every OS not listed in `[scripts]` uses. `xfce`
needs different logic per init system and package manager, so it overrides
three of its four supported OSes and leaves `ubuntu` on the default
`install.sh`:

```toml
os = ["alpine", "ubuntu", "debian", "arch"]
script = "install.sh"

[scripts]
alpine = "install-alpine.sh"
debian = "install-debian.sh"
arch = "install-arch.sh"
```

Compare `install.sh` (ubuntu/debian, `apt-get` + systemd `getty@.service`
override) against `install-alpine.sh` (`apk` + busybox `/etc/inittab`
autologin, no systemd unit exists to override). The packages and init
mechanism differ enough per OS that a single script branching internally
would be harder to read than one script per OS family.

Two conventions worth copying from the bundled scripts:

- **`set -e` at the top**, so the script stops on the first real failure
  instead of limping through with half the state applied.
- **A live-vs-disk check at the end**, since a diskless/live VM's root is a
  tmpfs or overlay in RAM and everything the script just installed is gone on
  reboot:

  ```sh
  root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)
  case "$root_fstype" in
  tmpfs | overlay)
      echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot."
      ;;
  *)
      echo "installed on a disk VM (root is $root_fstype), so this survives a reboot."
      ;;
  esac
  ```

  A recipe that silently promises persistence it can't deliver on a live VM
  is a worse failure than one that says so out loud.

## Testing a recipe manually

`stoat recipes` lists what's applicable to a given OS/backend, filtered by
`os`/`requires`:

```
stoat recipes --os alpine
stoat recipes --os alpine --backend apkovl
```

`stoat check-recipes` explains *why* a named recipe would or wouldn't apply,
useful while iterating on `os`/`requires`:

```
stoat check-recipes docker xfce --os alpine
```

Once a VM exists, apply the recipe for real and watch it stream:

```
stoat apply myvm --only xfce
```

Omitting `--only` runs every recipe recorded on the VM (`vm.toml`'s
`recipes` list). To inspect exactly what would be pushed without touching the
VM, read the resolved script directly:

```
cat ~/.stoat/recipes/xfce/install-alpine.sh
```

and to test the script's own logic in isolation, `sh -n script.sh` catches
syntax errors before it ever reaches a VM, or ssh in and pipe it by hand:

```
ssh -p <port> root@localhost 'sh -s' < ~/.stoat/recipes/docker/install.sh
```

## Where user recipes live

`~/.stoat/recipes/` (override with `STOAT_HOME`). Bundled recipes are copied
here on first run and refreshed on upgrade, but only if the on-disk copy
still matches what stoat last wrote (tracked in a `.manifest` file); a
hand-edited recipe is left alone. Drop a new directory with its own
`recipe.toml` anywhere under `~/.stoat/recipes/` and it's picked up the same
way, no code changes or registration needed.

Old-format flat files (`<name>.<os>.sh`, `<name>.cloud.yaml`) still work
during the v1-to-v2 deprecation period, but stoat logs a warning whenever one
is offered, suggesting migration to a `recipe.toml` directory.
