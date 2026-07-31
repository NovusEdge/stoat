# Troubleshooting

Symptom-first. Find the error text you're seeing and jump to it.

## `ssh not reachable on port N after 1m30s`

```
<name>: ssh not reachable on port <N> after 1m30s
```

(`internal/sshx/sshx.go`'s `Wait`, with `WaitTimeout` set to 90 seconds.)

**If this happens while provisioning a disk VM:** the apkovl that installs
your ssh key and enables `sshd` (`internal/apkovl/apkovl.go`) is built
**live-mode only** — `internal/qemu/run.go`'s `Start` only calls
`apkovl.Build` when `v.Mode == "live"`. A disk VM boots its installer ISO with
no key baked in and no `sshd` running, so there is nothing on the other end of
that port until you actually install the guest OS.

**Fix:** open the QEMU window for the VM, run the installer at the console
(`setup-alpine` on Alpine; check the ISO's own installer otherwise), then back
in stoat press `i` on the detail screen to mark the VM installed. Only after
that will ssh — and provisioning — have anything to connect to. Pressing `p`
before that point is refused outright, with a message pointing at the same
fix, rather than making you wait out the full timeout to find out:

```
<name>: not installed yet — run <installer> in the qemu window, then press i (marks it installed) before provisioning
```

## A binary on `/mnt/host` won't run — confusing "not found"

You built a binary on the host, it shows up fine on the shared
`/mnt/host` (backed by `-virtfs local,...,security_model=none` —
`internal/qemu/args.go`), but running it inside the guest fails with something
like:

```
sh: ./mybinary: not found
```

even though the file is right there and executable. This is almost always a
libc mismatch, not a missing file: your host binary is dynamically linked
against **glibc**, and stock Alpine is a **musl** system with no
`/lib64/ld-linux-x86-64.so.2` — the ELF interpreter path baked into a
glibc-linked binary. The kernel can't find that interpreter to load the
binary, and the resulting error reads exactly like "file not found" even
though the file is there.

**Fixes**, in order of how little they touch:

- Build statically instead: `CGO_ENABLED=0 go build` (for Go binaries) removes
  the dynamic libc dependency entirely.
- Add a glibc compatibility shim on the guest: `apk add gcompat`.
- Sidestep it: use a glibc-based cloud VM (Ubuntu/Debian/Fedora via the
  cloudinit backend) instead of an Alpine guest.

## `xorriso is required for cloud-init provisioning; install libisoburn`

Exact string, from `internal/cloudinit/cloudinit.go`'s `Seed` (via
`haveXorriso`, which just checks `exec.LookPath("xorriso")`). Building the
cloud-init seed ISO shells out to `xorriso`, and stoat doesn't vendor it.

**Fix:** install the package that provides `xorriso` — on most distros that's
`libisoburn` (the message names it directly), e.g. `apk add xorriso` on
Alpine or your distro's equivalent of `libisoburn`/`xorriso`.

## `/dev/kvm not usable`

```
/dev/kvm not usable: <err> (are you in the kvm group?)
```

(`internal/qemu/run.go`, from an `os.OpenFile("/dev/kvm", os.O_RDWR, 0)`
probe.) stoat always passes `-enable-kvm` — there's no software-emulation
fallback — so it needs read/write access to `/dev/kvm` before it will start
anything.

**Fix:** add yourself to the group that owns `/dev/kvm` (commonly `kvm`) and
start a new login session (`newgrp kvm`, or log out and back in) so the group
membership actually takes effect. Confirm with `ls -l /dev/kvm` and `groups`.

## A live VM lost everything after a reboot

This isn't a bug — it's how live mode works. A live VM's root filesystem is a
`tmpfs`/`overlay` mount that only exists in RAM for that boot
(`internal/apkovl/apkovl.go`'s doc comment, and the same detection every
bundled recipe uses: `awk '$2 == "/" { print $3 }' /proc/mounts` reporting
`tmpfs` or `overlay`). Every package you installed, every file you edited,
is gone the moment the VM restarts — only the apkovl itself (rebuilt fresh on
every `Start`, per `internal/qemu/run.go`) survives, and it can't carry
installed packages.

**If you want state to survive a reboot, live mode is the wrong mode.** Create
a **disk** VM instead (install the OS once, then it persists like a normal
machine) or a **cloud** VM (a prebuilt image where cloud-init's `packages:`/
`runcmd:` apply once at first boot and then stick).

## A VM shows as `broken` in the list

```
<name>: broken vm.toml — cannot start (d to delete)
```

A VM's directory exists under the data root but its `vm.toml` doesn't parse
(`internal/config/config.go`'s `ListBroken`). stoat still shows it — rather
than hiding a directory it can't fully understand — so you know it's there and
can act on it, but it can't be started, edited, or provisioned in that state.
Its reserved ssh port stays held (`FreePort` checks broken VMs' raw `vm.toml`
port fields too), so a broken VM won't silently let a new VM reuse its port
out from under it.

**Fix:** either hand-edit the `vm.toml` in that VM's directory to fix whatever
made it unparseable, or delete the directory — press `d` on it in the list and
confirm.

## `stoat rm` refuses to delete a running VM

```
stoat: rm: <name> is running; stop it first
```

(`internal/cli/cli.go`'s `runRM`, gated on `qemu.Running(v)`.) `rm` won't
delete a VM out from under a live QEMU process.

**Fix:** stop the VM first (`stoat down <name>`, or `d` in the TUI once it's
stopped), then `rm` it.
