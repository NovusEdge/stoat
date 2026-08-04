# TUI Reference

`stoat` with no subcommand launches the interactive terminal UI (`internal/tui`). It needs a terminal at least **60 columns by 20 rows**; anything smaller shows `terminal too small: resize to at least 60x20` instead of a corrupted layout.

Global rules that hold on every screen:

- **ctrl+c** quits immediately, from any screen and from inside any y/N prompt.
- **?** toggles the footer between its short form (one line) and full form (a bordered help panel), except while a text field has focus, where `?` is just a character being typed.
- The list screen's row/pane widths are fixed; they don't resize as VMs, names, or filters change.

## Screens

| Screen | Opened from | What it's for |
|---|---|---|
| [List](#list-screen) | startup | Browse, start/stop, ssh, provision, delete VMs |
| [Detail](#detail-screen) | `→`/`l` on a VM in the list | Inspect one VM, edit it, tail its last provision run |
| [New VM form](#new-vm-form) | `n` on the list | Create a VM: pick/download an image, size it, pick recipes |
| [Edit form](#edit-form) | `e` on the detail screen | Change an existing VM's mode, size, ports, recipes |

There is also a fifth, non-bubbletea "screen": `E` on the detail screen shells out to `$EDITOR` (or `vi`) on the VM's raw `vm.toml`, for anything the edit form doesn't expose.

## List screen

The list shows every VM directory under the data root as one sequence: valid VMs first, then any directory whose `vm.toml` fails to parse ("broken"), so a broken entry is visible and deletable rather than silently missing.

### Row format

A good VM's row is `name  mode  RAMMc  cpus` followed by either a dim `-` (stopped) or `up <uptime>  :<sshport>` (running):

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
| `d` | Delete | On a stopped VM or a broken entry, arms a `delete <name>? y/N` prompt. On a running VM, refuses with "stop `<name>` first". |
| `j`/`↓`, `k`/`↑`, `pgup`, `pgdown`, `home`, `end`, `g`, `G` | Move / page | Forwarded to the underlying list component. |
| `esc` | Clear search, or cancel a pending prompt | See ordering note below. |
| `q` | Quit | |
| `ctrl+c` | Quit | |
| `?` | Toggle help | |

**Prompt handling.** While the delete confirmation (`delete <name>? y/N`) or the auto-provision offer (see below) is showing, *every* key is consumed by that prompt: `y` confirms, anything else cancels it. This is why `n` doesn't fall through to "new VM" while a prompt with an "N" option is up. If a search filter is applied *and* a delete prompt is pending, pressing `esc` cancels the delete but leaves the filter in place: the delete-prompt check runs before the filter-clearing check, which is the safer of the two orderings.

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
| `s` | ssh into the VM | Running only, else "not running". |
| `p` | Provision | Same guard chain as the list screen. |
| `esc` / `←` / `h` / `q` | Back to list | |
| `?` | Toggle help | |
| `ctrl+c` | Quit | |

If no VM is selected (e.g. the list was empty), the screen just shows "no vm selected" and `esc` still returns.

## New VM form

Nine possible fields, only some of which are shown (and reachable by tab) at any moment: conditional fields are omitted from the tab order entirely rather than shown-but-disabled, so focus can never land on something invisible.

**Tab order:** name → image → *(backend override, BYO images only)* → *(mode, only when the resolved backend is `apkovl`)* → ram → cpus → *(disk size, only in disk mode)* → share → recipes.

The image picker offers every catalog entry (Alpine, etc.) plus any file already sitting under `isos/` that doesn't match a catalog entry ("byo"). A catalog entry not yet downloaded shows `⤓ download`; downloaded ones show `downloaded`.

### The two keys people get backwards

- **`space`, not `enter`, downloads an image.** With focus on the image row, `space` fetches the selected catalog image (or re-verifies/repairs it if already local, a mismatched checksum triggers a full refetch). `enter` never downloads; it tries to build the VM, and fails with "press space to download `<os>` first" if the image isn't local yet.
- **`E` on the detail screen opens the raw `vm.toml`; `e` opens the edit form.** (This applies to the detail screen, not the new-VM form, noted here because it's the same "did you mean the capital one" mistake.)

### Keys

| Key | Action | Notes |
|---|---|---|
| `tab` / `↓` | Next field | |
| `shift+tab` / `↑` | Previous field | |
| `←` / `→` | Change the focused field | Image row: cycle images. Backend row (BYO only): cycle `ssh` / `apkovl` / `cloudinit`. Mode row: toggle live/disk. Recipes row: move the sub-cursor. |
| `space` | Download image, or toggle a recipe | On the image row: download/repair the selected catalog image (no-op with a message on a BYO image, since those are already local; no-op while a fetch is already running). On the recipes row: check/uncheck the recipe under the sub-cursor. |
| `enter` | Create the VM | Validates the form (see below) and, on success, writes `vm.toml` (and allocates the disk image in disk mode) and returns to the list. |
| `esc` | Cancel | Back to the list, discarding the form. |
| `?` | Toggle help | Only when a non-text field has focus. |
| `ctrl+c` | Quit | |

**Validation on `enter`:** name is required, must contain no spaces or slashes, and must not already exist; an image must be selected and already downloaded; RAM must be a number ≥ 256 (MB); CPUs must be ≥ 1. The ssh port is picked automatically from the first free port.

Recipes offered are filtered to those matching the selected image's OS/backend, and the selection resets whenever the image changes.

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

The in-TUI editor for a VM that already exists: everything the raw `$EDITOR` round trip on `vm.toml` used to be needed for, minus the fields deliberately left out. Editable fields: **mode**, **ram**, **cpus**, **disk size** (hidden entirely in live mode), **share**, **ssh port**, and **recipes** (hidden if none match the VM's OS/backend). Each changed field shows a dim `← was <old value>` marker next to it.

### Keys

| Key | Action | Notes |
|---|---|---|
| `tab` / `↓` | Next field | |
| `shift+tab` / `↑` | Previous field | |
| `←` / `→` | Change the focused field | Mode row: cycle `live` / `disk` / `cloud` (also resyncs the recipe list, since recipes are per-backend). Recipes row: move the sub-cursor. |
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
| Port collision | Refused if another VM (including a broken one whose `vm.toml` won't parse but whose port is still committed to disk) already uses that port. |
| Switching to `cloud` mode | Refused unless the VM already has a base image: "cloud mode needs a base image: create a new VM from the catalog instead." |
| Switching away from `cloud` mode | Refused if the VM has no ISO of its own: "`<mode>` mode needs an iso, but this vm only has a cloud base image." |
| Disk size, disk mode | Required if empty. |
| Shrinking the disk | Refused, a qcow2 can only grow: "disk can only grow (`<old>` → `<new>` would destroy data)." |
| Growing/creating the disk while running | Refused: "stop `<name>` first: switching to disk mode has to create its disk" / "stop `<name>` before resizing its disk (nothing was saved)." |
| Resizing a cloud overlay that doesn't exist yet | Refused: "start `<name>` once before growing its disk (nothing was saved)", the overlay is created lazily on first boot. |

A save on a **running** VM is allowed for RAM/CPUs/ssh-port changes, but the pane warns "running: ram/cpus/ssh apply on restart" since they don't take effect until the VM is restarted. If nothing differs from what's on disk, the pane shows "no changes" instead of a save button state.

## Provisioning progress

Pressing `p` (list or detail) runs `startProvision`, which refuses before ever starting anything if: the VM is in `cloud` mode (recipes apply automatically via cloud-init at first boot, recreate the VM to change them), the VM is a disk VM not yet marked installed (run the installer at the console, then `i`), the VM has no recipes selected, or a provision run for that VM is already in flight.

Once running, a line appears above the status line on both the list and detail screens for every VM currently provisioning:

```
⠋ work · xfce · Unpacking libx11-data... · 1m32s
```

That's a spinner, the VM name, the current step (a recipe name parsed from the `=== recipe NAME ===` markers `sshx.Provision` writes to `last-provision.log`, or `starting`/`waiting for ssh` before one begins), the most recent real output line (truncated to 34 characters), and elapsed time. There is deliberately no percentage or progress bar: nothing knows how many steps a recipe has left.

### Auto-provision offer

After starting a VM that has recipes, stoat watches in the background for its ssh to come up (this does **not** block the UI) and then asks, rather than provisioning automatically:

```
work is up, run xfce now? y/N
```

`y` starts the same provision run `p` would; anything else declines with "not provisioning `<name>`, press p when you want to". The offer is skipped for cloud VMs, for disk VMs not yet installed, and, for disk/cloud VMs only, whenever the previous provision run already finished cleanly (a live VM's root is wiped by every reboot, so it's always offered again).

## Download progress

While the new-VM form is downloading a catalog image, a block appears under the form's fields, indented to line up with the field values:

```
  download alpine…
           [██████████████████████░░░░░░░░]  71%
           731 MiB / 1.0 GiB · 6.2 MiB/s · 45s left
```

If the server didn't send a `Content-Length`, the bar and percentage are omitted entirely (a fabricated percentage would be worse than none) and only a running byte count is shown.

## Terminal size and layout

The whole program refuses to render below **60×20** and shows a centered warning instead. Content taller than the terminal is anchored to the top rather than centered, so nothing scrolls out of reach off the top edge.
