# TUI reference

`stoat` with no subcommand launches the interactive terminal UI. It requires a
terminal that is at least **60 columns by 20 rows**. A smaller terminal shows
`terminal too small: resize to at least 60x20`.

Global rules that hold on every screen:

- **ctrl+c** quits immediately from any screen or confirmation prompt.
- **?** toggles the footer between its short form and full help panel. When a
  text field has focus, it enters a question mark in that field.
- The list screen uses fixed row and pane widths.

## Screens

| Screen | Opened from | What it's for |
|---|---|---|
| [List](#list-screen) | startup | Browse, start/stop, ssh, provision, delete VMs |
| [Detail](#detail-screen) | `→`/`l` on a VM in the list | Inspect one VM, edit it, tail its last provision run |
| [New VM form](#new-vm-form) | `n` on the list | Create a VM: pick/download an image, size it, pick recipes |
| [Edit form](#edit-form) | `e` on the detail screen | Change resources, sharing, display, ports, and recipes |

Press `E` on the detail screen to open the VM's raw `vm.toml` file in
`$EDITOR`, or in `vi` when `$EDITOR` is not set.

## List screen

The list shows every VM directory under the data root as one sequence: valid VMs first, then any directory whose `vm.toml` fails to parse ("broken"), so a broken entry is visible and deletable rather than silently missing.

### Row format

A valid VM row contains the name, mode, RAM, CPU count, and state. A stopped
VM shows `-`. A running VM shows `up <uptime> :<sshport>`:

```
❯ ● work           live   4096M  4c   up 2h15m  :2222
  ○ scratch        disk   2048M  2c   -
```

`●`/`○` mark running/stopped. A broken entry instead shows the parse error:

```
  ✗ oldvm          broken: unexpected token
```

### Keys

| Key | Action | Notes |
|---|---|---|
| `enter` | Start / stop the selected VM | Running → stops; stopped → starts. On a broken entry: "cannot start (d to delete)". |
| `→` / `l` | Open detail screen | Only for a valid VM; on a broken entry shows the same "cannot start" message. |
| `s` | ssh into the VM | Only when running, else status becomes "not running". Dimmed in the footer whenever the selected VM isn't ssh-able. |
| `p` | Provision | Runs the same guard chain as the detail screen's `p`, see [Provisioning](#provisioning-progress). |
| `/` | Search | Opens the filter input; typing filters the list by VM name. |
| `n` | New VM | Opens the new-VM form. If a download is still in flight from a previous visit to the form, that same form (and its download) is reused instead of being reset. |
| `r` | Edit recipes | Opens the recipes directory in `$EDITOR` (or `vi` when `$EDITOR` is unset). A recipe added there appears the next time the new-VM form opens. |
| `d` | Delete | On a stopped VM or a broken entry, arms a `delete <name>? y/N` prompt. On a running VM, refuses with "stop `<name>` first". |
| `j`/`↓`, `k`/`↑`, `pgup`, `pgdown`, `home`, `end`, `g`, `G` | Move / page | Forwarded to the underlying list component. |
| `esc` | Clear search, or cancel a pending prompt | See ordering note below. |
| `q` | Quit | |
| `ctrl+c` | Quit | |
| `?` | Toggle help | |

**Prompt handling.** While the delete confirmation (`delete <name>? y/N`) is
visible, `y` confirms it and any other key cancels it. If a search filter is
active, pressing `esc` cancels the confirmation but does not clear the filter.

**Search.** `/` opens a filter input that owns the keyboard (so `n`/`d`/`p`/`s`/`q` type into it rather than triggering their bindings). Once a filter is applied, the line under the list reads:

```
search "wor": 1 of 3 · esc clears
```

`esc` at that point clears the filter and restores the full list.

## Detail screen

Shows one VM's facts in a titled pane: mode + running/stopped state, a one-line mode hint, the ISO path, disk size and on-disk usage (disk mode only, with an "installed" yes/no), the ssh target (`user@127.0.0.1:port`), the share path if set, and the recipe names if any. Below that, a "last provision" pane tails the last 10 lines of `last-provision.log`, refreshed once a second while the screen is open.

### Keys

| Key | Action | Notes |
|---|---|---|
| `e` | Open the edit form | |
| `E` | Open raw `vm.toml` in `$EDITOR` | Falls back to `vi` if `$EDITOR` is unset. On return, the VM is reloaded from disk; a reload failure is shown as a status message. |
| `i` | Toggle "installed" | Disk-mode VMs only, "installed only applies to disk vms" otherwise. Marks a disk VM as having had its OS installed at the console, which is a prerequisite for provisioning it. |
| `d` | Cycle display preference | Cycles auto, window, and VNC. The change applies at the next start. |
| `s` | ssh into the VM | Running only, else "not running". |
| `p` | Provision | Same guard chain as the list screen. |
| `L` | Open console log | Opens a scrollable view of the end of `console.log`. |
| `S` | Manage snapshots | Opens the snapshot list for a disk or cloud VM. |
| `t` | Type console password | Available for a running VM that has a console password. |
| `c` | Copy console password | Copies the password through the terminal clipboard protocol. |
| `esc` / `←` / `h` / `q` | Back to list | |
| `?` | Toggle help | |
| `ctrl+c` | Quit | |

If the list is empty, the detail screen shows `no vm selected`. Press `esc` to
return.

## New VM form

The form hides fields that do not apply to the selected image. Hidden fields
are also omitted from the tab order.

**Tab order:** name → image → *(backend and OS, BYO images only)* → *(mode,
`apkovl` only)* → ram → cpus → *(disk size, disk and cloud modes)* → share →
display → recipes → *(console password, cloud-init only)*.

The image picker groups catalog entries by guest OS. The BYO group includes
unmatched image files from `isos/` and can search for disk images under the
home directory or accept a pasted path. A catalog image that is not available
locally shows `⤓ download`; a local image shows `downloaded`.

### Image and edit keys

- Press `←` or `→` on the image row to open the image picker.
- Press `space` on the image row to download or verify the selected catalog
  image. `enter` attempts to create the VM and reports
  `press space to download <os> first` when the image is not available.
- On the detail screen, press `e` to open the edit form or `E` to open the raw
  `vm.toml` file.

### Keys

| Key | Action | Notes |
|---|---|---|
| `tab` / `↓` | Next field | |
| `shift+tab` / `↑` | Previous field | |
| `←` / `→` | Change the focused field | Image row: open the image picker. Backend row (BYO only): cycle `ssh` / `apkovl` / `cloudinit`. Mode row: toggle live/disk. Display row: cycle auto/window/VNC. Recipes row: move the sub-cursor. |
| `space` | Download image, or toggle a recipe | On the image row: download/repair the selected catalog image (no-op with a message on a BYO image, since those are already local; no-op while a fetch is already running). On the recipes row: check/uncheck the recipe under the sub-cursor. |
| `enter` | Create the VM | Validates the form (see below) and, on success, writes `vm.toml` (and allocates the disk image in disk mode) and returns to the list. |
| `esc` | Cancel | Back to the list, discarding the form. |
| `?` | Toggle help | Only when a non-text field has focus. |
| `ctrl+c` | Quit | |

**Validation on `enter`:** name is required, must contain no spaces or slashes, and must not already exist; an image must be selected and already downloaded; RAM must be a number ≥ 256 (MB); CPUs must be ≥ 1. The ssh port is picked automatically from the first free port.

Recipes offered are filtered to those matching the selected image's guest OS
and manifest requirements. The compatibility `--backend` filter is not used
for manifest applicability. The selection resets whenever the image changes.

While a download is running, a progress block appears under the fields, see [Download progress](#download-progress).

### Console password row (cloud images only)

When the selected image uses the `cloudinit` backend, the form grows a
`console` row:

```
  console  (•) stoat  ( ) random
           console login for the stoat user
```

`←`/`→` or `space` toggles it. **stoat** is the fixed, documented password;
**random** generates 32 hex characters from `crypto/rand`. This password is
for the QEMU window only: the seed sets `ssh_pwauth: false`, so it is
refused over the forwarded SSH port. See
[Access and auth](../concepts/access-and-auth.md).

Cloud images lock every account by default, so without this the QEMU console
shows a login prompt with no valid answer.

> **The credentials are not valid the instant the VM boots.** The login
> prompt appears within seconds; the `stoat` user is created when cloud-init
> runs, which on a VM with packages in its seed can take minutes. Until then
> the prompt rejects everything. `cloud-init status` inside the guest, or a
> successful `s`/`stoat ssh`, tells you it has finished.

## Edit form

The edit form changes **ram**, **cpus**, **disk size**, **share**, **SSH port**,
**display**, and **recipes**. Disk size is hidden in live mode. Recipes are
hidden when none match the VM's OS and backend. The VM name, image, OS, mode,
and backend are immutable. Create a replacement VM to change one of those
values. Each changed field shows a `← was <old value>` marker.

### Keys

| Key | Action | Notes |
|---|---|---|
| `tab` / `↓` | Next field | |
| `shift+tab` / `↑` | Previous field | |
| `←` / `→` | Change the focused field | Display row: cycle auto/window/VNC. Recipes row: move the sub-cursor. |
| `space` | Toggle a recipe | Recipes row only. |
| `enter` | Save | Runs the guard chain below; on success writes `vm.toml` and returns to the detail screen. |
| `esc` | Cancel | Back to the detail screen without saving. |
| `?` | Toggle help | Only when a non-text field has focus. |
| `ctrl+c` | Quit | |

### Save guards

`enter` can refuse a save for any of these reasons, shown inline rather than applied silently:

| Guard | Refusal |
|---|---|
| RAM | Must be a number of MB, at least 256. |
| CPUs | Must be at least 1. |
| SSH port | Must be between 1024 and 65535. |
| Port collision | Refused if another VM, including one with an invalid `vm.toml`, already uses that port. |
| Disk size, disk mode | Required if empty. |
| Shrinking the disk | Refused, a qcow2 can only grow: "disk can only grow (`<old>` → `<new>` would destroy data)." |
| Growing the disk while running | Refused: "stop `<name>` before resizing its disk (nothing was saved)." |
| Resizing a cloud overlay that doesn't exist yet | Refused: "start `<name>` once before growing its disk (nothing was saved)", the overlay is created lazily on first boot. |

A save on a **running** VM is allowed unless it must resize the disk. Runtime
changes take effect after the next restart. Recipe-list changes affect the next
apply operation.

## Provisioning progress

Pressing `p` on the list or detail screen starts an apply operation. The TUI
refuses the operation when a disk VM is not installed, the VM has no selected
recipes, or an apply operation for that VM is already running.

Once running, a line appears above the status line on both the list and detail screens for every VM currently provisioning:

```
⠋ work · xfce · Unpacking libx11-data... · 1m32s
```

The line contains a spinner, the VM name, the current step, the latest output
line, and elapsed time. Stoat does not show a percentage because recipes do
not report their total number of steps.

### Automatic apply

After starting a live VM that has recipes, Stoat waits for SSH in the
background and applies the recipes automatically. The TUI remains usable while
it waits. After an unattended Alpine disk installation completes, Stoat
restarts the VM and follows the same process. Cloud-init applies a cloud VM's
initial recipes during first boot. Press `p` to apply pending or changed work
later.

## Download progress

While the new-VM form is downloading a catalog image, a block appears under the form's fields, indented to line up with the field values:

```
  download alpine…
           [██████████████████████░░░░░░░░]  71%
           731 MiB / 1.0 GiB · 6.2 MiB/s · 45s left
```

If the server omits `Content-Length`, Stoat omits the bar and percentage and
shows only the downloaded byte count.

## Terminal size and layout

The whole program refuses to render below **60×20** and shows a centered warning instead. Content taller than the terminal is anchored to the top rather than centered, so nothing scrolls out of reach off the top edge.
