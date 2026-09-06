# Recipe Spec v2 (historical draft)

Status: **historical proposal**. This document records an earlier design and
does not define all current behavior. For the shipped authoring workflow, see
[Recipes](recipes/overview.md), [Writing your own recipe](recipes/writing-your-own.md),
and the [current sample](reference/samples/recipe.toml).

The implementation currently accepts schema 2 and schema 3 manifests. Schema
3 adds parameters, secrets, outputs, and health checks. The current CLI also
provides `stoat recipe new`, remote recipe lock/sync, and project recipe
scopes; those shipped details are documented in the links above and in
[Sharing recipes](recipes/sharing.md). Statements below describe the draft
unless they are explicitly marked as current behavior.

## Summary

The draft proposed directories containing a `recipe.toml` manifest and one or
more shell scripts. The directory and manifest format is now shipped, but the
draft's backend and stage details are historical.

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
runtime = "sh"  # "sh" | "python3", interpreter the script runs under. Default: "sh"

# Optional: OS-specific script overrides
[scripts]
alpine = "install-alpine.sh"
fedora = "install-fedora.sh"
# unlisted OSes fall back to `script`
```

### Fields in the draft

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Short identifier, matches directory name |
| `description` | string | yes | One-line human description |
| `os` | string[] | no | Guest OSes this works on. Empty = all. |
| `requires` | string[] | no | Capabilities needed (see below) |
| `stage` | string | no | When to run: `install` or `provision`. Default: `provision` |
| `script` | string | yes | Default script path, relative to recipe dir |
| `scripts` | table | no | Per-OS script overrides |
| `runtime` | string | no | Interpreter to run the script under: `sh` or `python3`. Default: `sh` |

### Capabilities

Capabilities are facts about a guest that a recipe can require: every
`capabilities` entry and `init` of a loaded guest; see `stoat guest show`.

A recipe declaring `requires = ["systemd"]` is not offered to Alpine. Stoat resolves these against `guest.OS` at list/check time.

## Stages

### `provision` (default, draft model)

Runs after the VM is booted and reachable over SSH. This is the common case: install packages, configure services, etc.

- **apkovl/ssh backend**: Pushed over SSH via the recipe's `runtime` (`sh -s` by default, `python3 -` for `runtime = "python3"`).
  A non-`sh` runtime is bootstrapped first: stoat checks the guest for it and installs it with the guest's package manager if missing, over a separate SSH call, before piping the recipe body.
- **cloudinit backend**: Wrapped into a cloud-config `runcmd` block at VM creation, runs at first boot.

### `install` (draft model)

Reserved for initial disk setup, before the first real boot. Alpine disk-mode VMs automate `setup-alpine` unattended without needing a recipe (see below); install-stage recipe bodies are not executed today.

- **apkovl backend (disk mode)**: stoat bakes the answerfile and local.d install script for every uninstalled disk VM, from VM config alone.
- **cloudinit backend**: N/A (cloud images are pre-installed).
- **ssh backend**: N/A (assumes already installed).

## Execution Model

### For apkovl/ssh backends (draft model)

```
VM boots
  -> SSH becomes reachable
  -> stoat runs provision-stage recipes over SSH
  -> marks them applied in vm.toml
```

### For cloudinit backend (draft model)

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

### Unattended install (Alpine disk mode)

```
VM creation (disk mode)
  -> stoat bakes into apkovl (every uninstalled disk VM):
     - /etc/local.d/stoat-install.start
     - /etc/stoat/answerfile (generated from VM config)
  -> VM boots into live environment
  -> local.d script runs: setup-alpine -f /etc/stoat/answerfile
  -> script sets Installed=true flag somehow (writes to virtio-9p share?)
  -> script reboots
  -> VM boots from disk
  -> provision-stage recipes run
```

## Reboot Behavior

A recipe declares `reboot = true` in its manifest to mark that the guest needs a reboot before its changes take effect. Examples: switching init systems, loading a new kernel module.

Stoat reboots the guest once, after every recipe in the apply run finishes. The reboot happens at the end of the run, not after the individual recipe that requested it. If several recipes in the run declare `reboot = true`, stoat still reboots only once.

This reboot applies to disk-mode VMs only. Live-mode VMs run their root filesystem on tmpfs, so a reboot wipes it. A live recipe that needs to restart a session restarts it in place instead, for example with `kill -HUP 1`.

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

## Migration (historical proposal)

### Bundled recipes

Convert existing files:

| Old | New |
|-----|-----|
| `xfce.alpine.sh` | `xfce/install-alpine.sh` + manifest with `os = ["alpine"]` or `[scripts] alpine = ...` |
| `xfce.cloud.yaml` | `xfce/install.sh` (extract the runcmd lines) |
| `xfce.fedora.cloud.yaml` | `xfce/install-fedora.sh` |

One `xfce/` directory replaces 6+ files.

### User recipes (historical proposal)

The proposal suggested a deprecation period for old-format recipes. Current
Stoat reads directory manifests; old flat recipe files are not a supported
authoring format. Use `stoat recipe new` or convert a recipe to the current
directory layout.

## Decisions recorded by the historical proposal

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
