# Writing your own recipe

Use `stoat recipe new <name>` to create a recipe directory from the annotated
sample. It creates `recipe.toml`, the default script, and scripts named by the
sample's `[scripts]` table. Edit the files, then select the recipe for a VM
whose guest matches its `os` and `requires` fields.

```sh
stoat recipe new tools --os alpine
stoat recipe show tools
stoat recipes --os alpine
```

The generated directory is under `~/.stoat/recipes/` (or the `recipes/`
directory below `STOAT_HOME`). Project caches are reserved for remote recipes
managed by `recipe lock` and `recipe sync`. `recipe new` accepts `--backend
cloudinit` for compatibility, but recipes always use the manifest directory
format and shell scripts.

## Manifest

The manifest needs `name` and `script`. `schema` defaults to 2. Set
`schema = 3` to use parameters, outputs, or health checks.

```toml
schema = 3
name = "tools"
description = "Install the tools used by this VM"
os = ["alpine", "ubuntu"]
requires = []
stage = "provision"
script = "install.sh"
runtime = "sh"
run = "once"
reboot = false
depends = []

[params.editor]
type = "string"
default = "vi"
help = "editor command to install"

[params.port]
type = "int"
default = 8080

[params.debug]
type = "bool"
default = false

[params.channel]
type = "enum"
values = ["stable", "test"]
default = "stable"

[params.token]
type = "secret"
required = true
help = "token used by the installer"

[outputs]
installed_path = "path of the installed tools"

[health]
check = "tools --version"
timeout = "30s"
```

Manifest rules:

| Field | Rule |
|---|---|
| `name` | Required recipe identifier. Use a simple name without spaces, slashes, backslashes, a leading dot, `.` or `..`; Stoat also rejects an empty name. `recipe new` additionally rejects dots in the name. |
| `description` | Optional text shown by `recipes` and `recipe show`. |
| `schema` | 2 or 3. A missing value means 2. Parameters, outputs and health require 3. |
| `os` | Optional list of guest names. Empty means every loaded guest. |
| `requires` | Optional capability list. Every capability must match the guest. |
| `stage` | `provision` (default) is supported. `install` is parsed but cannot run. |
| `script` | Required path relative to the recipe directory. |
| `scripts` | Optional OS or guest-alias overrides of `script`. |
| `runtime` | `sh` (default) or `python3`; SSH provisioning invokes `sh -s` or `python3 -`. |
| `run` | `once` (default), `always`, or `manual`. |
| `auto` | Accepted manifest metadata. The current TUI auto-provision decision does not read it. |
| `reboot` | Reboot a disk VM once after the apply run succeeds. Live VMs are not rebooted. |
| `depends` | Recipe names that must run first and must be in the VM's recipe list or already applied. |

## Parameters and secrets

Parameter names match `[a-z][a-z0-9_]*`. Supported types are `string`, `int`,
`bool`, `enum`, and `secret`. Every non-secret parameter needs a `default` or
`required = true`. An enum needs a non-empty `values` list, and its default
must be one of those values. A secret cannot have a default.

Stoat resolves a value from the VM's stored non-secret override or the
manifest default. It reads secrets from the VM's `secrets.toml`; for a
project-declared VM, the project source is `.stoat/secrets.toml` and Stoat
reconciles those values into the VM data directory. Secret files are mode
0600 and values are never included in `vm.toml`, the applied record, or JSON
output. A required value with no value fails before the script runs.

The script receives resolved values as environment variables named
`STOAT_PARAM_<UPPERCASE_NAME>`, plus `STOAT_RECIPE`. For example, `editor`
becomes `STOAT_PARAM_EDITOR`. Secret parameters are also passed to the guest
through that environment, so do not print them.

Set values when creating or updating a VM:

```sh
stoat create tools-vm --image alpine-virt --recipes tools \
  --set tools.editor=vim --secret tools.token
stoat update tools-vm --set tools.port=9090
stoat update tools-vm --unset tools.port
```

Interactive `--secret` reads a value without displaying it. In JSON mode,
provide `STOAT_SECRET_TOOLS_TOKEN` in the environment because JSON mode never
prompts. `--unset` clears a non-secret override and restores its manifest
default; it does not clear a secret.

## Outputs and health

Stoat creates `STOAT_OUTPUT` for each recipe run. Write one `name=value` line
per output. Stoat records declared and undeclared output names after a
successful run; undeclared names are reported in the apply log. It removes
the temporary guest output file after reading it.

```sh
#!/bin/sh
set -e

stoat_pkg_setup
stoat_pkg_install htop
printf 'installed_path=%s\n' /usr/bin/htop >> "$STOAT_OUTPUT"
```

`[health] check` runs after the recipe's apply step. The command exits 0 for a
healthy result. `timeout` is a positive Go duration such as `30s`; it defaults
to 30 seconds when a check exists. `stoat wait <vm> --healthy` waits for every
applied recipe that declares a check and records `ok`, `failed`, or `unknown`
health in the VM status.

## Guest prelude

For a loaded guest, Stoat prepends a shell prelude. Use these portable verbs
when one script supports several package managers or init systems:

| Name | Effect |
|---|---|
| `stoat_pkg_setup` | Refreshes the package index when the guest defines a setup command. |
| `stoat_pkg_install <packages...>` | Installs packages with the guest's package manager. |
| `stoat_svc_enable <name>` | Enables a service at boot. |
| `stoat_svc_start`, `stoat_svc_stop`, `stoat_svc_restart`, `stoat_svc_status` | Controls one service. |
| `STOAT_OS` | Loaded guest name. |
| `STOAT_INIT` | Guest init value, such as `systemd` or `openrc`. |
| `STOAT_PKGMGR` | Package manager basename, such as `apk` or `apt-get`. |

Recipes run as the VM's SSH user, with the guest's escalation command when
that user is not root. Bundled guests provide the appropriate commands. A
recipe that needs root should use the service and package verbs instead of
assuming `sudo` exists.

## Persistence and scripts for several guests

A live Alpine VM stores its root in a temporary `tmpfs` or `overlay`; package
changes and files disappear after reboot. A disk VM keeps those changes. State
the behavior in a recipe that changes the guest:

```sh
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)
case "$root_fstype" in
tmpfs | overlay)
    echo "NOTE: this is a live VM (root is $root_fstype, in RAM); changes are lost after reboot."
    ;;
*)
    echo "installed on a disk VM (root is $root_fstype); changes survive reboot."
    ;;
esac
```

Use `[scripts]` when package names or setup differ:

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

`stoat recipes --os <guest>` lists matching recipes. `stoat check-recipes`
explains why a named recipe does not match. The `--backend` flag is accepted
for compatibility; recipe applicability is determined by the guest OS and
manifest requirements. The VM backend determines how the selected script is
executed, not whether its manifest matches.

## Cloud-init recipes

Cloud-mode VMs run selected recipe scripts from their first-boot NoCloud seed.
Stoat writes each script and executes the scripts in selection order, then
writes a marker after each successful script. A later `apply` can discover
those markers and populate the VM's applied state.

Cloud-init currently wraps recipe bodies in a shell command and does not run
the SSH runtime bootstrap. Use `runtime = "sh"` for a cloud recipe; a
`python3` manifest does not install Python or invoke `python3` in cloud mode.
Cloud-init secrets are written to a temporary mode-0600 file in the guest and
removed after the seed commands finish.

## Check and apply

Inspect the contract, syntax-check a script with the guest shell, then plan an
apply before running it:

```sh
stoat recipe show tools
sh -n ~/.stoat/recipes/tools/install.sh
stoat apply tools-vm --dry-run
stoat apply tools-vm --only tools
stoat wait tools-vm --healthy
```

`stoat apply` requires a running VM. It stops at the first failed recipe and
does not mark that recipe as applied. A dependency cycle, missing manifest,
invalid parameter, dirty remote checkout, or stale project lock fails before
the guest run begins.
