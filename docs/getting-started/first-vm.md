# Your First VM

Create an Alpine **live** VM, apply a recipe, and connect over SSH. Live mode
needs no OS installation and discards the guest session when it stops. Disk
and cloud VMs are covered at the end of this page.

Before you start, make sure `stoat doctor` reports `ok`, see [Installation](installation.md).

## 1. Launch the TUI

```sh
stoat
```

On a fresh install you'll see stoat's banner over an empty list. The footer
shows the list screen's keys: `↵ start/stop`, `→/l details`, `s ssh`,
`p apply`, `/ search`, `n new`, `d delete`, `q quit`, `? help`.

The example below shows the list after creating three VMs:

![The VM list with two running guests and one stopped guest](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-list.png)

## 2. Press `n` for a new VM

This opens the "new vm" form. It starts you on the **name** field, and
Alpine is preselected in the **image** row. Alpine is the default catalog
entry and the only OS whose live mode works without an install step.

Use `work` as the name for this walkthrough. `tab`/`↓` and `shift+tab`/`↑`
move between fields; `←`/`→` changes a picker's value.

![The new VM form](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-create.png)

## 3. Download the image

Tab down to **image** and press `space`: the `⤓ download` marker means it isn't local yet. stoat fetches the latest Alpine ISO and checksum-verifies it against Alpine's published index:

```
  download alpine…
           ████████████████░░░░░░░░░░░░░░░░  51%
           238 MiB / 467 MiB · 9.4 MiB/s · 24s left
```

When it finishes, the status line reports `downloaded isos/alpine-standard-....iso` and the image row switches to `downloaded`. (Pressing space again later just re-verifies the file and only re-downloads it if it no longer matches.)

## 4. Leave the rest at their defaults and create it

`mode` is already `live` (the radio toggle only matters for Alpine: every other image's mode is fixed by its backend). RAM (4096 MB), CPUs (4), and share (`~/vms`, exposed inside the guest as `/mnt/host`) are all fine to leave as-is for a first VM. If you want a desktop or a couple of tools installed automatically, move to **recipes** and press `space` on `xfce`, `docker`, `devtools`, or `tailscale` to check them.

Press `enter` (from anywhere in the form) to create the VM. If the image hasn't finished downloading yet, stoat tells you instead of creating a broken VM (`press space to download alpine first`).

You're back on the list screen with a `created <name>` status message and your
new VM listed as stopped.

## 5. Start it

With the VM selected, press `enter`. stoat rebuilds the VM's Alpine overlay
(this wires in the SSH key and networking) and launches QEMU. The status line
reports that it started. Press `l` to see its state, forwarded SSH port,
and apply log. This capture uses the example VM `alpine-desktop`:

![VM detail screen](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-details.png)

When the `xfce` recipe finishes on a graphical host, its desktop appears in a
QEMU window:

![Alpine XFCE in a QEMU window](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/qemu-xfce.png)

The [23-second walkthrough video](https://github.com/NovusEdge/stoat/blob/main/assets/demo.mp4) shows the TUI list,
details, and the resulting XFCE window.

**If you selected any recipes** in step 4, stoat watches for sshd in the background (you can keep using the TUI while it waits), and once the guest answers, it asks:

```
work is up, run xfce now? y/N
```

Press `y` to apply the selected recipes. The detail screen's **last apply**
pane shows their output. Press another key to skip; `p` starts an apply later.
On later boots, the host's recipe records can cause a `run = "once"` recipe
to be skipped after its guest files have disappeared. See the
[live restart limitation](../troubleshooting.md#a-live-vm-lost-everything-after-a-reboot).

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

Stop it with `enter` again (or `stoat down <name>`) whenever you're done: as a live VM, everything inside it is gone the moment it stops.

## Disk and cloud VMs

Live isn't the only mode: the other two keep guest state.

- **Disk** VMs boot from the same kind of ISO and keep a qcow2 disk that survives restarts. An Alpine disk VM installs itself on its first start with a generated `setup-alpine` answer file. `stoat up` waits for the installer to power off, starts the VM from the new disk, and then offers or runs its recipes. The unattended install can take up to 15 minutes. The generated overlay carries stoat's SSH key into the installed system. For a non-Alpine BYO ISO, run that guest's installer at the console, add stoat's public key to the configured SSH account, then stop and start the VM. `i` on the detail screen still flips `installed` by hand when automatic detection is wrong.
- **Cloud** VMs (Ubuntu, Debian, Fedora, Arch, or Alpine cloud) use a prebuilt cloud image instead of an ISO. stoat writes a cloud-init seed on first start that creates a `stoat` user, installs stoat's key for it, and applies the selected recipe content during that first boot. After SSH becomes available, the normal apply pass can record the results and apply pending or changed recipe work over SSH. Pressing `p` runs that normal apply path; it does not rebuild the first-boot seed. SSH in as `stoat@127.0.0.1:<port>` rather than `root`. Building that seed needs `xorriso` on your `PATH` (see [Installation](installation.md)).

For a repository with multiple VMs, use the [project workflow](../guides/project-workflow.md) instead of recreating each VM in the TUI.
