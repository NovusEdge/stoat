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
  docker/
    recipe.toml
    install.sh
    install-alpine.sh
    install-debian.sh
    install-fedora.sh
    install-arch.sh
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

`internal/recipes/bundled/xfce/recipe.toml`, one script covering every OS by
branching on the guest verbs (see Verbs, below):

```toml
name = "xfce"
description = "XFCE desktop with autologin startx on tty1"
os = ["alpine", "ubuntu", "debian", "arch"]
stage = "provision"
script = "install.sh"
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
| `runtime` | string | no | `"sh"` | Interpreter the script runs under: `"sh"` or `"python3"` |

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

- **apkovl/ssh backends**: the script is piped into the declared runtime over
  ssh, `sh -s` by default.
- **cloudinit backend**: the script is wrapped into the cloud-config
  `runcmd` block at VM creation and runs at first boot.

## Runtime

`runtime` picks the interpreter a `provision`-stage script runs under. It
defaults to `"sh"`, always present in a POSIX guest. Setting it to
`"python3"` lets a recipe write a real Python script instead of shell.

Before running a `python3` recipe, stoat checks the guest for `python3` and
installs it with the guest's package manager if missing (`apk`, `apt-get`,
`pacman`, or `dnf`, matching the `requires` capability table above). The
check and install happen once, over ssh, right before the recipe body itself
runs, piped into `python3 -`.

A minimal Python recipe, `recipe.toml`:

```toml
name = "hello-py"
description = "prints the guest hostname with a Python script"
os = ["alpine", "ubuntu"]
runtime = "python3"
script = "install.py"
```

and `install.py`:

```python
import socket
print("hello from", socket.gethostname())
```

### `install`

Reserved for initial disk setup, before the first real boot. Alpine disk-mode
VMs already run `setup-alpine` unattended on first boot from a config-derived
answerfile (`internal/apkovl`), so no recipe is needed to install the OS.
Install-stage recipe bodies are not executed today; the stage is accepted by
the manifest parser but does nothing yet.

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

## Verbs

A recipe script does not need to know each guest's package manager or init
system by name: the prelude `stoat` renders in front of every script body
defines a small set of shell functions and variables over the guest's
`guest.toml` facts (`docs/reference/guest.md`), the same functions for every
OS.

| Name | What it does |
|------|--------------|
| `stoat_pkg_setup` | Refreshes the package index (a no-op where none is needed, e.g. dnf) |
| `stoat_pkg_install <pkgs...>` | Installs packages with the guest's package manager |
| `stoat_svc_enable <name>` | Enables a service to start at boot |
| `stoat_svc_start`/`stop`/`restart`/`status` `<name>` | Controls a running service |
| `STOAT_OS` | The guest name, e.g. `alpine` |
| `STOAT_INIT` | `systemd`, `openrc`, or `rc` |
| `STOAT_PKGMGR` | The package manager binary, e.g. `apk`, `apt-get`, `dnf`, `pacman` |

`internal/recipes/bundled/xfce/install.sh` is one script for four OSes,
branching on `STOAT_PKGMGR` for package names and `STOAT_INIT` for the
autologin mechanism (systemd's `getty@.service` drop-in vs. Alpine's busybox
`/etc/inittab`):

```sh
stoat_pkg_setup

case "$STOAT_PKGMGR" in
apk)     stoat_pkg_install xfce4 xfce4-terminal dbus-x11 ;;
apt-get) stoat_pkg_install xfce4 xfce4-terminal dbus-x11 xinit xserver-xorg ;;
pacman)  stoat_pkg_install xfce4 xfce4-terminal dbus xorg-xinit ;;
esac

stoat_svc_enable dbus
```

`[scripts]` per-OS overrides (below) still exist for a recipe whose install
differs by more than package names and a service call, `docker`'s GPG key
setup and repo URL being a different string per distro family.

## OS-specific script overrides

`script` is the fallback every OS not listed in `[scripts]` uses. `docker`
needs a different script per distro family (repo URL, GPG key path), so it
overrides every OS it supports:

```toml
os = ["alpine", "ubuntu", "debian", "fedora", "arch"]
script = "install.sh"

[scripts]
alpine = "install-alpine.sh"
ubuntu = "install-debian.sh"
debian = "install-debian.sh"
fedora = "install-fedora.sh"
arch = "install-arch.sh"
```

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
