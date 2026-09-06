# Modes and Backends

Every VM in stoat has a **mode** (`live`, `disk`, or `cloud`) and a
**provisioning backend** (`apkovl`, `cloudinit`, or `ssh`). The two are
related but not the same thing:

- **Mode** (`vm.toml`'s `mode` field) decides how QEMU is invoked and what
  happens to the guest's storage. It's what stoat's QEMU argument builder
  branches on when constructing the `qemu-system-x86_64` command line, and
  what its start logic checks before launching a VM.
- **Backend** (`vm.toml`'s `backend` field) identifies how stoat prepares the
  VM and provisions its selected recipes. It is chosen at creation time and
  remains part of the VM's runtime configuration; edit it only as part of a
  deliberate migration.

**`disk` mode's first boot depends on the image.** Alpine disk VMs install
unattended; a bring-your-own image needs its own console install and SSH key
setup. Stoat applies recipes only after the installed guest is ready. See the
section below.

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

A `disk` VM has a real, persistent `disk.qcow2`. Alpine disk VMs install
themselves on their first start. Stoat generates a `setup-alpine` answer file,
bakes it into the boot overlay, waits for the installer to power off, and
starts the VM again from the installed disk. The CLI waits up to 15 minutes
for this sequence. A failed install leaves the installer running so you can
inspect the console and logs.

Until the Alpine install completes, the SSH service belongs to the temporary
installer environment. The TUI and CLI refuse to apply recipes to it. After
the next start sees enough data on the qcow2 disk, stoat sets `installed =
true` and stops attaching the ISO. The `i` key on the detail screen can still
toggle this field when automatic detection is wrong.

A non-Alpine BYO ISO does not have Stoat's Alpine answer file. Install that OS
at its console, add `~/.stoat/id_stoat.pub` to the account named by
`vm.toml`'s `sshuser`, then stop and start the VM. Stoat can provision it only
after the OS and SSH access are ready.

### cloud

A `cloud` VM starts from a downloaded cloud image (Ubuntu, Debian, Fedora,
Arch, or Alpine cloud; see the image catalog) rather than an installer ISO.
stoat creates a
copy-on-write overlay backed by that shared base image, so the multi-hundred
-megabyte download only happens once no matter how many `cloud` VMs you spin
up from it.

Alongside the overlay, stoat builds a small NoCloud cloud-init seed ISO
(volume label `CIDATA`) containing:

- a `stoat` user, password-less sudo, and stoat's public key
- the selected recipes' cloud-init fragments and script bodies, as separate
  archive documents

Cloud-init applies the seed **at first boot only**. The seed carries selected
recipe fragments and scripts as cloud-init archive documents. Stoat may run a
post-boot apply pass to discover and record those results. The TUI's provision
key and `stoat apply` still use the normal recipe run policy, so a changed
script or a recipe with pending work can run over SSH later. Stoat does not
rebuild the seed on later starts. Changing the recipe list requires recreating
the VM so the new list is present in the seed.

The host seed artifacts remain in the VM directory for inspection and later
diagnosis. Stoat creates seed directories with mode `0700` and seed files with
mode `0600` before writing their bytes; it does not promise to delete or
detach those artifacts after boot.

## Comparison

| | `live` | `disk` | `cloud` |
|---|---|---|---|
| Storage | none, RAM only | `disk.qcow2`, persistent | `disk.qcow2`, CoW overlay over a shared base image |
| Survives reboot | no | yes | yes |
| SSH on first boot | yes, automatically | Alpine: after unattended install; BYO: after your install | yes, via cloud-init (first boot only) |
| Who installs the OS | nobody, it's the live ISO | Alpine: stoat; BYO: you, at the console | nobody, image is prebuilt |
| Recipes applied | over SSH, on demand (`p`) | over SSH, on demand (`p`), only once `installed` | cloud-init fragments at first boot; pending or changed work over SSH |
| Rebuilt on every start | yes (the apkovl) | no | no (overlay created once) |
| Typical backend | `apkovl` | `apkovl` (Alpine, undeployed) or `ssh` (any other ISO) | `cloudinit` |

## The three provisioning backends

`backend` in `vm.toml` records which of these the create form used, mainly
so it can filter which recipes to offer and (for a bring-your-own image)
guess the right mode:

- **`apkovl`**: Alpine only. Produces the `live`-mode overlay described
  above. An Alpine image can be run either as `live` or as `disk` mode (you
  choose at creation); either way, the backend is recorded as `apkovl`.
- **`cloudinit`**: any recognized cloud image (Ubuntu, Debian, Fedora, Arch,
  or Alpine cloud). Always resolves to `cloud` mode; there's no other option.
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
