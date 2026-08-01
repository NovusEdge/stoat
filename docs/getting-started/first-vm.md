# Your First VM

This walks through creating and using an Alpine **live** VM end to end: launch the TUI, download an image, create the VM, start it, provision it, and ssh in. Live is the mode to start with — it needs no manual steps inside the guest, unlike disk or cloud VMs (covered at the end of this page).

Before you start, make sure `stoat doctor` reports `ok` — see [Installation](installation.md).

## 1. Launch the TUI

```sh
stoat
```

On a fresh install you'll see stoat's banner over an empty list:

```
no vms yet — press n to create one
```

with a footer showing the list screen's keys: `↵ start/stop`, `→/l details`, `s ssh`, `p provision`, `/ search`, `n new`, `d delete`, `q quit`, `? help`.

## 2. Press `n` for a new VM

This opens the "new vm" form. It starts you on the **name** field, and Alpine is preselected in the **image** row (it's the default catalog entry, and the only OS whose live mode works without an install step):

```
❯ name     work
  image    alpine   apkovl     ⤓ download
  mode     (•) live   ( ) disk
           runs in RAM · ssh works now · reboot wipes it

  ram      4096 MB
  cpus     4
  share    ~/vms

  recipes  [ ] devtools  [ ] docker  [ ] tailscale  [ ] xfce
```

Type a name (letters/digits, no spaces or slashes). `tab`/`↓` and `shift+tab`/`↑` move between fields; `←`/`→` changes a picker's value.

## 3. Download the image

Tab down to **image** and press `space` — the `⤓ download` marker means it isn't local yet. stoat fetches the latest Alpine ISO and checksum-verifies it against Alpine's published index:

```
  download alpine…
           ████████████████░░░░░░░░░░░░░░░░  51%
           238 MiB / 467 MiB · 9.4 MiB/s · 24s left
```

When it finishes, the status line reports `downloaded isos/alpine-standard-....iso` and the image row switches to `downloaded`. (Pressing space again later just re-verifies the file and only re-downloads it if it no longer matches.)

## 4. Leave the rest at their defaults and create it

`mode` is already `live` (the radio toggle only matters for Alpine — every other image's mode is fixed by its backend). RAM (4096 MB), CPUs (4), and share (`~/vms`, exposed inside the guest as `/mnt/host`) are all fine to leave as-is for a first VM. If you want a desktop or a couple of tools installed automatically, move to **recipes** and press `space` on `xfce`, `docker`, `devtools`, or `tailscale` to check them.

Press `enter` (from anywhere in the form) to create the VM. If the image hasn't finished downloading yet, stoat tells you instead of creating a broken VM (`press space to download alpine first`).

You're back on the list screen, with a `created <name>` status message and your new VM showing:

```
❯ ○ work           live   4096M  4c  —
```

## 5. Start it

With the VM selected, press `enter`. stoat rebuilds the VM's Alpine overlay (this is what wires in the SSH key and networking) and launches QEMU. The status line reports `<name> started`, and the row's dot turns solid to show it's running, with an uptime and its forwarded ssh port:

```
● work           live   4096M  4c  up 12s  :2200
```

**If you selected any recipes** in step 4, stoat watches for sshd in the background — you can keep using the TUI while it waits — and once the guest answers, it asks:

```
work is up — run xfce now? y/N
```

Press `y` to run them (this streams into the same file the detail screen's "last provision" pane tails), or anything else to skip (`not provisioning work — press p when you want to`) — the prompt reappears every time you (re)start a live VM, since a reboot wipes whatever ran before.

If you didn't select any recipes, there's nothing to offer, and you go straight to step 6.

## 6. SSH in

Press `s`. stoat suspends the TUI and hands the terminal to a real `ssh` process (using the keypair it generated at `~/.stoat/id_stoat`, connecting as `root`), dropping you at a root shell inside the guest. `exit` that shell and you're back in the TUI.

The same connection is available from a plain shell without the TUI at all:

```sh
stoat ssh work
```

## Checkpoint

You should now have:

- [ ] `stoat doctor` reporting `ok`
- [ ] an Alpine **live** VM created, downloaded, and started
- [ ] (optionally) its recipes provisioned, either via the automatic offer or `p`
- [ ] an ssh session into it, from the TUI (`s`) or the CLI (`stoat ssh <name>`)

Stop it with `enter` again (or `stoat down <name>`) whenever you're done — as a live VM, everything inside it is gone the moment it stops.

## Disk and cloud VMs

Live isn't the only mode — the other two exist because they keep guest state, at the cost of a manual step live doesn't need:

- **Disk** VMs boot from the same kind of ISO but keep a qcow2 disk that survives restarts. You install the OS yourself at the QEMU console (`setup-alpine`, for an Alpine disk image) and then restart the VM: the next start sees an OS on the disk, marks the VM `installed` and stops booting the ISO. Until then, `s` and `p` refuse with a message pointing you at that same step. An Alpine disk VM carries the same apkovl as a live one *while installing*, so `setup-disk` copies your ssh key onto the installed system for you. `i` on the detail screen still flips `installed` by hand when the guess is wrong.
- **Cloud** VMs (Ubuntu, Debian, Fedora, Arch) use a prebuilt cloud image instead of an ISO. stoat writes a cloud-init seed on first start that creates a `stoat` user, installs stoat's key for it, and — unlike live/disk — applies any selected recipes automatically as part of that same first boot, with no offer or `p` needed (pressing `p` on a cloud VM just tells you so). SSH in as `stoat@127.0.0.1:<port>` rather than `root`. Building that seed needs `xorriso` on your `PATH` (see [Installation](installation.md)).
