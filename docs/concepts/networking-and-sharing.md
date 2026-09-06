# Networking and sharing

## User-mode networking

Every Stoat VM uses QEMU user-mode networking (`-netdev user`) instead of a
bridge or tap device. This configuration does not require root privileges or
host bridge configuration. The guest is behind NAT and is reachable from the
host only through explicit port forwards.

Stoat always forwards the guest's SSH port (22) to a host-side loopback port.
You can declare additional host-to-guest TCP forwards in the project file or
VM configuration. Every forward is bound explicitly to
`127.0.0.1`, not `0.0.0.0`: QEMU's default would otherwise publish every
guest's SSH port to the LAN.

## Port allocation

Each VM gets a host port in the range **2200–2299**, recorded as `sshport`
in its `vm.toml`. When creating a new VM, Stoat picks the first port in that
range that is:

1. Not already claimed by another VM's `sshport`, and
2. Actually bindable on loopback right now.

Stoat also checks existing `vm.toml` files. A stopped VM does not hold its port
open, but the port remains assigned to that VM.

A **broken** `vm.toml` file can also reserve its port. Stoat attempts to read
the `sshport` value from the file even when the full TOML document cannot be
parsed. This prevents a new VM from receiving the same port. See
[The data root](data-root.md).

## The legacy `share` field

Setting `share` on a VM exposes a host directory to the guest over 9p, as
`/mnt/host`. Stoat exports this directory read-only. The guest can remount it
with `rw`, but QEMU still rejects writes. Stoat uses `security_model=mapped-xattr`
so guest-created symlinks cannot escape into the host filesystem.

How the share is mounted depends on the VM mode:

- **`live`** mode wires it up automatically. The apkovl adds an `/etc/fstab`
  entry for the 9p mount and enables the `localmount` init script at boot,
  so `/mnt/host` is available when the VM starts.
- **`disk`** guests receive the same `-virtfs` device from QEMU. Before a
  recipe apply, Stoat adds the mount to `/etc/fstab` and mounts it. To use the
  share before the first apply, or on a VM without recipes, mount it in the
  guest:

  ```sh
  mkdir -p /mnt/host
  mount -t 9p -o trans=virtio,version=9p2000.L,ro host /mnt/host
  ```

  Add the entry to `/etc/fstab` to mount the share after later boots.

  **`cloud`** guests receive cloud-init mount entries for the writable work
  share, the legacy `share` when configured, and project shares. Debian cloud
  images skip these entries because their cloud kernel has no 9p module.

## Project shares

A project declaration can list `shares` under `[vms.<key>]`. Stoat resolves
each entry relative to the directory containing `stoat.toml` and refuses a
path that leaves the project, including through a symlink. `.` mounts at
`/work`; a subdirectory mounts at `/work/<basename>`. These exports are
separate from the legacy `share` export and are read-write inside the guest.

Debian cloud images do not mount project shares because their cloud kernel has
no 9p module. Ubuntu, Fedora, Arch, and Alpine use the guest's 9p support when
the definition permits it. See [The project file](../reference/project-file.md#shares)
for the declaration syntax.

## Sharing a binary built on the host: the musl/glibc trap

A dynamically linked binary built on a glibc-based host might not run in an
**Alpine** guest. Alpine uses musl. A glibc-linked binary can name this ELF
interpreter, which Alpine does not provide:

```
/lib64/ld-linux-x86-64.so.2
```

The kernel cannot find that path, so `execve()` fails with `ENOENT`, and the
shell reports something like:

```
$ ./mybinary
sh: ./mybinary: not found
```

The error refers to the missing interpreter, not to the binary itself.

### Fixes

Use one of these solutions:

- **Build a static binary.** For a Go binary, disable cgo:

  ```sh
  CGO_ENABLED=0 go build -o mybinary .
  ```

- **Install `gcompat` in the guest.** Alpine provides this glibc-compatibility
  package:

  ```sh
  apk add gcompat
  ```

- **Use a glibc-based guest.** A
  `cloud` VM (Ubuntu, Debian, Fedora, Arch; see [Modes and
  backends](modes-and-backends.md)) is glibc-based and does not have this
  incompatibility.
