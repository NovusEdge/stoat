# stoat

A terminal UI for local QEMU VMs. Alpine-first, no libvirt, no daemon.

## Quick start

```
make install
stoat
```

## Keys

### List

| Key | Action |
|-----|--------|
| `↵` | start / stop |
| `→` / `l` | details |
| `s` | ssh in (only works on a running VM) |
| `n` | new vm |
| `d` then `y` | delete (any other key cancels; prompts `y/N`) |
| `q` / `ctrl+c` | quit |

### Detail

| Key | Action |
|-----|--------|
| `e` | edit `vm.toml` in `$EDITOR` |
| `i` | toggle installed (disk VMs only) |
| `s` | ssh in (only works on a running VM) |
| `esc` | back to list |
| `ctrl+c` | quit |

## Live vs. disk VMs

VMs are either **live** or **disk**.

- **Live** VMs are diskless and disposable: they boot straight from the ISO
  and discard all state on stop. Nothing persists between runs.
- **Disk** VMs keep a qcow2 that survives restarts. Run `setup-alpine` once
  in the QEMU window, then press `i` on the detail screen to mark the VM
  installed.

## Phase 1 limitation

Live VMs boot without an apkovl, so they come up at a console login with
**no ssh access** — `s` will not work on them. This is expected in phase 1;
provisioning live VMs over ssh arrives in phase 2. Disk VMs are fully
usable today: run `setup-alpine` in the QEMU window, then press `i`.

## Data layout

Data lives in `~/.stoat` (override with `$STOAT_HOME`):

```
~/.stoat/
  isos/        downloaded ISO images
  recipes/     provisioning recipes
  <vm-name>/   one directory per VM, with a hand-editable vm.toml
```
