# Networking and Sharing

## User-mode networking

Every stoat VM gets QEMU's user-mode networking (`-netdev user`) rather than
a bridge or tap device. That means no root setup, no bridge configuration,
and no interference with your host's real network, but it also means the
guest is behind NAT, reachable from the host only through explicit port
forwards.

stoat forwards exactly one port per VM: the guest's SSH port (22) is mapped
to a host-side loopback port. The forward is bound explicitly to
`127.0.0.1`, not `0.0.0.0`: QEMU's default would otherwise publish every
guest's SSH to your LAN, which is not something a local dev VM tool should
do without asking.

## Port allocation

Each VM gets a host port in the range **2200–2299**, recorded as `sshport`
in its `vm.toml`. When creating a new VM, stoat picks the first port in that
range that is:

1. Not already claimed by another VM's `sshport`, and
2. Actually bindable on loopback right now.

The bindability check alone isn't enough: a VM that's been created but never
started holds nothing open on its port, so without also checking existing
`vm.toml`s, a fresh VM could easily be handed a port another (stopped) VM
already considers its own.

This is also why a **broken** `vm.toml` (one that exists but fails to parse,
see [The data root](data-root.md)) still has its port reserved: stoat
falls back to a best-effort regex over the raw file text to pull out the
`sshport` line even when the rest of the TOML won't decode. The port a
broken VM was using is very likely still committed to that VM's disk image,
so treating "unparseable" as "claims nothing" would silently hand the same
port to a second VM, exactly the collision this fallback exists to avoid.

## The `share` field

Setting `share` on a VM exposes a host directory to the guest over 9p, as
`/mnt/host`. QEMU is passed `security_model=none` for this: the only
unprivileged option: `passthrough` needs root, and `mapped-xattr` needs host
filesystem xattr support that isn't guaranteed to be there.

Whether the share actually gets *mounted* inside the guest depends on mode:

- **`live`** mode wires it up automatically. The apkovl adds an `/etc/fstab`
  entry for the 9p mount and enables the `localmount` init script at boot,
  so `/mnt/host` is there and mounted the moment the VM comes up, no action
  needed.
- **`disk`** and **`cloud`** guests get the same `-virtfs` device from QEMU,
  but nothing stoat controls injects an `/etc/fstab` entry for them. You
  need to mount it yourself once inside the guest, e.g.:

  ```sh
  mkdir -p /mnt/host
  mount -t 9p -o trans=virtio,version=9p2000.L,rw host /mnt/host
  ```

  (add that to `/etc/fstab` yourself if you want it to persist across
  reboots on a `disk` VM).

## Sharing a binary built on the host: the musl/glibc trap

If you build a binary on a typical glibc-based Linux host (Arch, Fedora,
Ubuntu, Debian) and drop it into your shared directory expecting to run it
straight from `/mnt/host` inside an **Alpine** guest (the default for `live`
mode), it will not execute. Alpine is musl-based, and a glibc-linked binary's
ELF header names an interpreter that simply doesn't exist there:

```
/lib64/ld-linux-x86-64.so.2
```

The kernel can't find that path, so `execve()` fails with `ENOENT`, and the
shell reports something like:

```
$ ./mybinary
sh: ./mybinary: not found
```

which reads exactly like a typo or a missing file, even though `ls` shows
the binary sitting right there with the executable bit set. What's actually
missing is the *interpreter*, not the file itself: a genuinely confusing
error to hit for the first time.

### Fixes

Pick whichever fits what you're sharing:

- **Build static.** If it's your own Go binary, build with cgo disabled so
  there's no dynamic interpreter to resolve at all:

  ```sh
  CGO_ENABLED=0 go build -o mybinary .
  ```

- **Install `gcompat` in the guest.** Alpine ships a glibc-compatibility
  shim package for exactly this case:

  ```sh
  apk add gcompat
  ```

- **Use a glibc-based guest instead.** If you're not tied to Alpine, a
  `cloud` VM (Ubuntu, Debian, Fedora, Arch; see [Modes and
  backends](modes-and-backends.md)) is glibc-based and won't hit this at
  all.
