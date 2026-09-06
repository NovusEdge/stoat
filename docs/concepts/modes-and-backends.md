# Modes and backends

Every VM in Stoat has a **mode** (`live`, `disk`, or `cloud`) and a
**provisioning backend** (`apkovl`, `cloudinit`, or `ssh`). The two are
related but have different purposes:

- **Mode** is the `vm.toml` `mode` field. It controls the guest storage and
  boot process.
- **Backend** is the `vm.toml` `backend` field. It controls how Stoat prepares
  the VM and applies its selected recipes.

Stoat selects both values when it creates the VM. To use a different image,
mode, or backend, create a replacement VM.

**`disk` mode's first boot depends on the image.** Alpine disk VMs install
unattended; a bring-your-own image needs its own console install and SSH key
setup. Stoat applies recipes only after the installed guest is ready. See the
section below.

## The three modes

### live

A `live` VM has no persistent guest disk. It boots the selected ISO into RAM,
and its root filesystem is an Alpine initramfs overlay. Stopping or restarting
the VM discards its guest state.

At each start, Stoat builds an Alpine overlay tarball called an apkovl and
provides it to the guest on a FAT-formatted auxiliary drive. The overlay
contains:

- Stoat's client public key, which enables key-based SSH access as
  `root@127.0.0.1:<port>`
- a stable host key, which prevents a host-key warning after Stoat rebuilds
  the VM
- `networking` and `sshd` enabled at boot, plus (if the VM has a `share`
  configured) the 9p mount at `/mnt/host` wired into `/etc/fstab`
  automatically

Because Stoat rebuilds the overlay at each start, a `live` VM does not retain
guest state between boots.

### disk

A `disk` VM has a real, persistent `disk.qcow2`. Alpine disk VMs install
themselves on their first start. Stoat generates a `setup-alpine` answer file,
bakes it into the boot overlay, waits for the installer to power off, and
starts the VM again from the installed disk. The CLI waits up to 15 minutes
for this sequence. A failed install leaves the installer running so you can
inspect the console and logs.

Until the Alpine install completes, the SSH service belongs to the temporary
installer environment. The TUI and CLI refuse to apply recipes to it. After
the next start sees enough data on the qcow2 disk, Stoat sets `installed =
true` and stops attaching the ISO. The `i` key on the detail screen can still
toggle this field when automatic detection is wrong.

A non-Alpine BYO ISO does not have Stoat's Alpine answer file. Install that OS
at its console, add `~/.stoat/id_stoat.pub` to the account named by
`vm.toml`'s `sshuser`, then stop and start the VM. Stoat can provision it only
after the OS and SSH access are ready.

### cloud

A `cloud` VM starts from a downloaded cloud image (Ubuntu, Debian, Fedora,
Arch, or Alpine cloud; see the image catalog) rather than an installer ISO.
Stoat creates a copy-on-write overlay backed by the shared base image.
Multiple VMs can therefore use one downloaded base image.

Alongside the overlay, Stoat builds a small NoCloud cloud-init seed ISO
(volume label `CIDATA`) containing:

- a `stoat` user, password-less sudo, and Stoat's public key
- cloud-init documents for the user, mounts, OS packages, and the selected
  recipe scripts

Cloud-init applies the seed **at first boot only**. The seed carries the
initial recipe fragments and scripts as cloud-init archive documents. A later
`stoat apply` operation discovers and records those results, then runs pending
or changed recipe work over SSH. Stoat does not rebuild the seed on later
starts.

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
| Who installs the OS | nobody, it is the live ISO | Alpine: Stoat; BYO: you, at the console | nobody, image is prebuilt |
| Recipes applied | checked over SSH after start; `p` on demand | pending work over SSH after start, once `installed`; `p` on demand | cloud-init at first boot; pending or changed work over SSH |
| Rebuilt on every start | yes (the apkovl) | no | no (overlay created once) |
| Typical backend | `apkovl` | `apkovl` (Alpine) or `ssh` (any other ISO) | `cloudinit` |

## The three provisioning backends

The `backend` field in `vm.toml` identifies how Stoat prepares and provisions
the VM:

- **`apkovl`**: Alpine only. Produces the `live`-mode overlay described
  above. An Alpine image can be run either as `live` or as `disk` mode (you
  choose at creation); either way, the backend is recorded as `apkovl`.
- **`cloudinit`**: any recognized cloud image (Ubuntu, Debian, Fedora, Arch,
  or Alpine cloud). Always resolves to `cloud` mode; there's no other option.
- **`ssh`**: the fallback for a bring-your-own image that Stoat does not
  recognize. Always resolves to `disk` mode: an unrecognized ISO is assumed
  to need a real install, followed by manual/SSH-based provisioning, since
  the apkovl live path only exists for Alpine.

Regardless of which backend created a `disk` VM, after it is installed and
marked as installed, provisioning uses the same process. Stoat pipes each
selected recipe's shell script over SSH into `sh -s` on the guest and streams
the output to `last-provision.log` (see [The data root](data-root.md)).

## See also

- [The data root](data-root.md): where each mode's files (`ovl/`,
  `disk.qcow2`, `last-provision.log`, ...) live on the host
- [Networking and sharing](networking-and-sharing.md): how the forwarded
  SSH port and the `/mnt/host` share interact with each mode
