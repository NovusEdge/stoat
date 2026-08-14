# Modes and Backends

Every VM in stoat has a **mode** (`live`, `disk`, or `cloud`) and a
**provisioning backend** (`apkovl`, `cloudinit`, or `ssh`). The two are
related but not the same thing:

- **Mode** (`vm.toml`'s `mode` field) decides how QEMU is invoked and what
  happens to the guest's storage. It's what stoat's QEMU argument builder
  branches on when constructing the `qemu-system-x86_64` command line, and
  what its start logic checks before launching a VM.
- **Backend** (`vm.toml`'s `backend` field) records how the create form
  picked recipes and provisioning at creation time. Once the VM exists,
  nothing at runtime dispatches on this field: it's informational.

**`disk` mode gives you no SSH access until you install an OS yourself and
tell stoat so.** See the section below.

## The three modes

### live

A `live` VM never touches a real disk. It boots the ISO you picked, straight
into RAM: the root filesystem is an Alpine initramfs overlay, and nothing
about it is written anywhere persistent. Stop or reboot the VM and you get a
clean slate next time.

What makes `live` mode useful: every time you start
one, stoat builds a small Alpine overlay tarball (an "apkovl") and hands it to
the guest as a fake FAT disk over `-virtfs`/vvfat. That overlay bakes in:

- stoat's own SSH keypair, so `root@127.0.0.1:<port>` works the moment sshd
  comes up, with no login prompt, no password, no manual key copying
- a stable host key, so a rebuilt VM (which happens on every single start,
  by design; see below) doesn't make your SSH client complain about a
  changed host key each time
- `networking` and `sshd` enabled at boot, plus (if the VM has a `share`
  configured) the 9p mount at `/mnt/host` wired into `/etc/fstab`
  automatically

Because the overlay is rebuilt from scratch on every start rather than
reused, a `live` VM never carries state forward between boots. That's the
trade for SSH working immediately with zero setup.

### disk

A `disk` VM has a real, persistent `disk.qcow2`. But stoat does not install
anything onto it for you. The first time you start a `disk` VM, it boots the
ISO you attached, the same as a bare-metal install would, and you install
the guest OS yourself, interactively, in the QEMU console window.

Until you do that, there is no operating system on the disk, which means
there is no sshd running and no key installed anywhere. **Provisioning
cannot work yet, because there is nothing on the other end of the SSH
connection.** Pressing the provision key against a fresh `disk` VM doesn't
time out mysteriously: stoat's `p` handler checks this up front and tells
you so directly, rather than waiting the full SSH-connect timeout to fail
with a generic "not reachable."

Two things need to happen before a `disk` VM becomes provisionable:

1. **Install the OS at the console**, then mark stoat's SSH keypair
   (`~/.stoat/id_stoat.pub`) as an authorized key inside the guest yourself,
   for whichever user `vm.toml`'s `sshuser` names (root, if unset); nothing
   in stoat injects it automatically for `disk` mode the way `apkovl.Build`
   does for `live`.
2. **Mark the VM installed**: press `i` on the VM's detail screen. This
   flips the `installed` field in `vm.toml` and, from then on, changes the
   QEMU boot order: with `installed = false`, the ISO stays attached and
   forced first on *every* boot (so you can always get back to the
   installer); once `installed = true`, the ISO is no longer attached at all
   and the VM boots straight off `disk.qcow2`.

`installed` only applies to `disk` mode; it's a no-op field for `live` and
`cloud` VMs.

### cloud

A `cloud` VM starts from a downloaded cloud image (Ubuntu, Debian, Fedora,
Arch; see the image catalog) rather than an installer ISO. stoat creates a
copy-on-write overlay backed by that shared base image, so the multi-hundred
-megabyte download only happens once no matter how many `cloud` VMs you spin
up from it.

Alongside the overlay, stoat builds a small NoCloud cloud-init seed ISO
(volume label `CIDATA`) containing:

- a `stoat` user, password-less sudo, and stoat's public key
- any cloud-flavored recipes you selected, merged into the seed's
  `packages:`/`runcmd:` lists

Cloud-init applies all of this **at first boot only**: there is no ongoing
SSH-based provisioning step for `cloud` VMs the way there is for `live`/`disk`.
Pressing the provision key on a `cloud` VM is a deliberate no-op: it tells you
recipes already ran at first boot, and that changing them means recreating
the VM (the seed isn't rebuilt on later starts, since by then the overlay
holds real guest state you don't want thrown away).

## Comparison

| | `live` | `disk` | `cloud` |
|---|---|---|---|
| Storage | none, RAM only | `disk.qcow2`, persistent | `disk.qcow2`, CoW overlay over a shared base image |
| Survives reboot | no | yes | yes |
| SSH on first boot | yes, automatically | **no**, until installed | yes, via cloud-init (first boot only) |
| Who installs the OS | nobody, it's the live ISO | you, at the console | nobody, image is prebuilt |
| Recipes applied | over SSH, on demand (`p`) | over SSH, on demand (`p`), only once `installed` | baked into the cloud-init seed at first boot |
| Rebuilt on every start | yes (the apkovl) | no | no (overlay created once) |
| Typical backend | `apkovl` | `apkovl` (Alpine, undeployed) or `ssh` (any other ISO) | `cloudinit` |

## The three provisioning backends

`backend` in `vm.toml` records which of these the create form used, mainly
so it can filter which recipes to offer and (for a bring-your-own image)
guess the right mode:

- **`apkovl`**: Alpine only. Produces the `live`-mode overlay described
  above. An Alpine image can be run either as `live` or as `disk` mode (you
  choose at creation); either way, the backend is recorded as `apkovl`.
- **`cloudinit`**: any recognized cloud image (Ubuntu, Debian, Fedora,
  Arch). Always resolves to `cloud` mode; there's no other option.
- **`ssh`**: the fallback for a bring-your-own image stoat doesn't
  recognize. Always resolves to `disk` mode: an unrecognized ISO is assumed
  to need a real install, followed by manual/SSH-based provisioning, since
  the apkovl live path only exists for Alpine.

Regardless of which backend created a `disk` VM, once it's installed and
marked so, provisioning always runs the same way: each selected recipe's
shell script is piped over SSH into `sh -s` on the guest, with output
streamed to `last-provision.log` (see [The data root](data-root.md)).

## See also

- [The data root](data-root.md): where each mode's files (`ovl/`,
  `disk.qcow2`, `last-provision.log`, ...) actually live on the host
- [Networking and sharing](networking-and-sharing.md): how the forwarded
  SSH port and the `/mnt/host` share interact with each mode
