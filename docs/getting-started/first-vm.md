# Your first VM

Create an Alpine **live** VM, apply a recipe, and connect over SSH. Live mode
needs no OS installation and discards the guest session when it stops. Disk
and cloud VMs are covered at the end of this page.

Before you start, run `stoat doctor`. Continue when it reports `ok`. See
[Installation](installation.md) if a check fails.

## 1. Launch the TUI

```sh
stoat
```

On first use, the TUI shows an empty VM list. The footer lists the available
keys: `↵ start/stop`, `→/l details`, `s ssh`,
`p apply`, `/ search`, `n new`, `d delete`, `q quit`, `? help`.

The example below shows the list after creating three VMs:

![The VM list with two running guests and one stopped guest](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-list.png)

## 2. Press `n` for a new VM

This opens the **new vm** form with the **name** field selected. The **image**
row defaults to Alpine, which is the only supported live-mode guest that does
not require an installation step.

Enter `work` as the VM name. Use `tab` or `↓` to move to the next field.
Use `shift+tab` or `↑` to move to the previous field. Use `←` and `→` to
change a selected value.

![The new VM form](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-create.png)

## 3. Download the image

Move to **image** and press `space`. The `⤓ download` marker means that the
image is not available locally. Stoat downloads the current Alpine ISO and
verifies its checksum against Alpine's published index:

```
  download alpine…
           ████████████████░░░░░░░░░░░░░░░░  51%
           238 MiB / 467 MiB · 9.4 MiB/s · 24s left
```

When the download finishes, the status line reports
`downloaded isos/alpine-standard-....iso`, and the image row changes to
`downloaded`. Pressing `space` again verifies the local file and downloads it
again only when the checksum does not match.

## 4. Leave the rest at their defaults and create it

Leave **mode** set to `live`. Other guest images use the mode selected by their
backend. For this walkthrough, keep the default RAM, CPU, and share settings:

- RAM: 4096 MB
- CPUs: 4
- Share: `~/vms`, mounted read-only in the guest at `/mnt/host`

To install optional software, move to **recipes** and press `space` on one or
more recipes. Available examples include `xfce`, `docker`, `devtools`, and
`tailscale`.

Press `enter` from any field to create the VM. If the image is not available,
Stoat stops and reports `press space to download alpine first`.

You're back on the list screen with a `created <name>` status message and your
new VM listed as stopped.

## 5. Start it

With the VM selected, press `enter`. Stoat rebuilds the Alpine overlay with
the SSH and network configuration, then starts QEMU. The status line confirms
that the VM started. Press `l` to view its state, forwarded SSH port, and apply
log. This capture uses the example VM `alpine-desktop`:

![VM detail screen](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-details.png)

When the `xfce` recipe finishes on a graphical host, its desktop appears in a
QEMU window:

![Alpine XFCE in a QEMU window](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/qemu-xfce.png)

The [23-second walkthrough video](https://github.com/NovusEdge/stoat/blob/main/assets/demo.mp4) shows the TUI list,
details, and the resulting XFCE window.

If you selected recipes in step 4, Stoat waits for SSH in the background and
then applies the recipes automatically. You can continue to use the TUI while
it waits. The **last apply** pane on the detail screen shows the output. Press
`p` to start another apply operation later.

On later boots, the host's recipe records can cause a `run = "once"` recipe
to be skipped after its guest files have disappeared. See the
[live restart limitation](../troubleshooting.md#a-live-vm-lost-everything-after-a-reboot).

If you did not select a recipe, continue to step 6.

## 6. SSH in

Press `s`. Stoat suspends the TUI and starts `ssh` with the private key at
`~/.stoat/id_stoat`. The live Alpine guest opens a root shell. Run `exit` to
close the SSH session and return to the TUI.

The same connection is available from a plain shell without the TUI at all:

```sh
stoat ssh work
```

## Checkpoint

Confirm these results:

- [ ] `stoat doctor` reporting `ok`
- [ ] an Alpine **live** VM created, downloaded, and started
- [ ] optional recipes applied automatically or with the `p` key
- [ ] an SSH session opened from the TUI or with `stoat ssh <name>`

To stop the VM, press `enter` or run `stoat down <name>`. Live mode discards
all guest changes when the VM stops.

## Disk and cloud VMs

Disk and cloud modes retain guest state across restarts.

### Disk VMs

A disk VM boots from an ISO and stores guest state in a qcow2 disk. An Alpine
disk VM installs itself on first start with a generated `setup-alpine` answer
file. The unattended installation can take up to 15 minutes. `stoat up` waits
for the installer to stop, starts the VM from its disk, and then offers or
applies its recipes.

For a non-Alpine bring-your-own ISO, complete the guest installer at the
console. Add Stoat's public key to the configured SSH account, then stop and
start the VM. If automatic installation detection is incorrect, press `i` on
the detail screen to change the `installed` state.

### Cloud VMs

Cloud VMs use a prepared image for Ubuntu, Debian, Fedora, Arch, or Alpine.
On first start, Stoat creates a cloud-init seed that adds the `stoat` user,
installs Stoat's public key, and includes the selected recipe content. Creating
the seed requires `xorriso`; see [Installation](installation.md).

After SSH becomes available, the normal apply operation records results and
runs pending or changed recipe work over SSH. Pressing `p` runs this operation;
it does not rebuild the first-boot seed. Connect as
`stoat@127.0.0.1:<port>` instead of `root`.

For a repository with multiple VMs, use the [project workflow](../guides/project-workflow.md) instead of recreating each VM in the TUI.
