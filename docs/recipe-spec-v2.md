# Recipe Spec v2

Status: **draft**

## Summary

Recipes become directories containing a `recipe.toml` manifest and one or more shell scripts. One format, one execution model, works on every backend.

## Directory Structure

```
~/.stoat/recipes/
  xfce/
    recipe.toml
    install.sh
  docker/
    recipe.toml
    install.sh
  devtools/
    recipe.toml
    install.sh
```

Bundled recipes ship under `internal/recipes/` with the same structure and get copied to the data root on first run (same Install() logic as today, respecting user edits).

## Manifest Schema

```toml
# recipe.toml

# Required
name = "xfce"
description = "XFCE desktop environment with lightdm"

# Targeting (all optional, empty = no restriction)
os = ["alpine", "ubuntu", "debian", "arch"]  # which guest OSes
requires = ["systemd"]                        # capabilities: systemd, apt, apk, etc.

# Execution
stage = "provision"  # "install" | "provision" (see Stages below)
script = "install.sh"  # relative path to the script

# Optional: OS-specific script overrides
[scripts]
alpine = "install-alpine.sh"
fedora = "install-fedora.sh"
# unlisted OSes fall back to `script`
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Short identifier, matches directory name |
| `description` | string | yes | One-line human description |
| `os` | string[] | no | Guest OSes this works on. Empty = all. |
| `requires` | string[] | no | Capabilities needed (see below) |
| `stage` | string | no | When to run: `install` or `provision`. Default: `provision` |
| `script` | string | yes | Default script path, relative to recipe dir |
| `scripts` | table | no | Per-OS script overrides |

### Capabilities

Capabilities are facts about a guest that a recipe can require:

| Capability | Meaning | OSes |
|------------|---------|------|
| `systemd` | Uses systemd init | ubuntu, debian, arch, fedora |
| `openrc` | Uses OpenRC init | alpine |
| `apt` | Has apt package manager | ubuntu, debian |
| `apk` | Has apk package manager | alpine |
| `dnf` | Has dnf package manager | fedora |
| `pacman` | Has pacman | arch |

A recipe declaring `requires = ["systemd"]` is not offered to Alpine. Stoat resolves these against `guest.OS` at list/check time.

## Stages

### `provision` (default)

Runs after the VM is booted and reachable over SSH. This is the common case: install packages, configure services, etc.

- **apkovl/ssh backend**: Pushed over SSH via `sh -s`, same as today.
- **cloudinit backend**: Wrapped into a cloud-config `runcmd` block at VM creation, runs at first boot.

### `install`

Runs during initial disk setup, before the first real boot. For Alpine disk mode, this means automating `setup-alpine`.

- **apkovl backend (disk mode)**: Baked into the apkovl as a local.d script that runs on the live boot, performs disk install, then reboots.
- **cloudinit backend**: N/A (cloud images are pre-installed).
- **ssh backend**: N/A (assumes already installed).

Only one `install`-stage recipe makes sense per VM. If multiple are selected, stoat errors at creation time.

## Execution Model

### For apkovl/ssh backends

```
VM boots
  -> SSH becomes reachable
  -> stoat runs provision-stage recipes over SSH
  -> marks them applied in vm.toml
```

### For cloudinit backend

```
VM creation
  -> stoat wraps provision-stage scripts into cloud-config:
     write_files:
       - path: /var/lib/stoat/recipes/xfce.sh
         permissions: '0755'
         content: |
           <script content>
     runcmd:
       - /var/lib/stoat/recipes/xfce.sh
  -> VM boots with seed
  -> cloud-init runs scripts at first boot
  -> (no post-boot apply needed)
```

### For install stage (Alpine disk mode)

```
VM creation (disk mode, install-stage recipe selected)
  -> stoat bakes into apkovl:
     - /etc/local.d/stoat-install.start
     - /etc/stoat/answerfile (generated from VM config)
  -> VM boots into live environment
  -> local.d script runs: setup-alpine -f /etc/stoat/answerfile
  -> script sets Installed=true flag somehow (writes to virtio-9p share?)
  -> script reboots
  -> VM boots from disk
  -> provision-stage recipes run
```

## State Tracking

Add to `vm.toml`:

```toml
[applied]
xfce = 2026-08-04T12:34:56Z
docker = 2026-08-04T12:35:00Z
```

This lets the TUI show:
- Pending: recipe in `recipes` but not in `applied`
- Applied: recipe in both
- Stale: recipe in `applied` but removed from `recipes`

## Migration

### Bundled recipes

Convert existing files:

| Old | New |
|-----|-----|
| `xfce.alpine.sh` | `xfce/install-alpine.sh` + manifest with `os = ["alpine"]` or `[scripts] alpine = ...` |
| `xfce.cloud.yaml` | `xfce/install.sh` (extract the runcmd lines) |
| `xfce.fedora.cloud.yaml` | `xfce/install-fedora.sh` |

One `xfce/` directory replaces 6+ files.

### User recipes

Old-format recipes (`.sh` files in recipes root) continue to work during a deprecation period. Stoat logs a warning suggesting migration.

## Decisions

1. **Auto-provision**: Toggle-able via a field in recipe.toml. Recipes can declare `auto = true` to run automatically when the VM becomes reachable for the first time. Default is `false` (require explicit Apply).

2. **Answerfile generation**: The answerfile is generated from VM config with sensible defaults:
   - `KEYMAPOPTS="us us"` (keyboard layout)
   - `HOSTNAMEOPTS="-n <vm-name>"` (from VM name)
   - `INTERFACESOPTS="auto lo\niface lo inet loopback\nauto eth0\niface eth0 inet dhcp"` (DHCP, already configured in apkovl)
   - `DNSOPTS="-d local"` (use DHCP-provided DNS)
   - `TIMEZONEOPTS="-z UTC"` (UTC timezone)
   - `PROXYOPTS="none"` (no proxy)
   - `APKREPOSOPTS="-1"` (first mirror, community enabled)
   - `SSHDOPTS="-c openssh"` (openssh, already running via apkovl)
   - `NTPOPTS="-c chrony"` (chrony for time sync)
   - `DISKOPTS="-m sys /dev/vda"` (sys mode to virtio disk)
   - `LBUOPTS="none"` (no lbu backup, sys mode doesn't need it)
   - `APKCACHEOPTS="/var/cache/apk"` (standard cache location)
   - Root password remains locked (SSH key auth only, key already in apkovl)

3. **Install stage signaling**: The install script writes a success marker to the 9p work share (`/mnt/work/.installed`). Stoat can poll for this file, or the script can also write status/errors there. On success, stoat updates `vm.toml` with `Installed = true`.

4. **Re-running recipes**: Behavior is per-recipe via a field in recipe.toml. Options:
   - `run = "once"` (default): Skip if already in `[applied]`
   - `run = "always"`: Re-run on every `stoat apply`
   - `run = "manual"`: Never auto-run, only via explicit `stoat apply --recipe <name>`

5. **Recipe versioning**: Recipes use semver in recipe.toml (`version = "1.0.0"`). The `[applied]` table in vm.toml tracks both timestamp and version. When a bundled recipe updates its version, stoat can detect the mismatch and prompt/auto-reapply based on the recipe's `run` setting.
