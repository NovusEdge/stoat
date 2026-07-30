# stoat

A terminal UI for local QEMU VMs. Alpine-first, no libvirt, no daemon.

## Setup

### Host requirements

Linux with KVM. stoat shells out to `qemu-system-x86_64` and `qemu-img` to run
and create VMs, `ssh` to connect into a running VM, and `$EDITOR` (falling
back to `vi`) to edit a VM's `vm.toml`.

On Arch:

```
sudo pacman -S --needed qemu-full openssh
```

(`qemu-desktop` also works and is smaller; either provides
`qemu-system-x86_64` and `qemu-img` with GTK/OpenGL display support.)

On Debian/Ubuntu:

```
sudo apt install qemu-system-x86 qemu-utils openssh-client
```

### KVM access

`/dev/kvm` must be readable and writable by your user. Check its group:

```
ls -l /dev/kvm
```

The group should be `kvm`, and your user should be a member of it:

```
id -nG
```

If your user isn't in the `kvm` group, add it and then **log out and back
in** (group membership is only picked up on new sessions):

```
sudo usermod -aG kvm "$USER"   # then log out and back in
```

### GPU/display

stoat runs QEMU with `-display gtk,gl=on`, so your QEMU build needs GTK and
OpenGL support (the `qemu-full`/`qemu-desktop` Arch packages and the Debian/
Ubuntu packages above provide this). This is currently a source-level
constant — stoat doesn't yet expose a setting to change it. If a VM fails to
start with a display/GL error, the workaround is to edit
`internal/qemu/args.go`, drop `,gl=on` from the `-display` argument, and
rebuild.

### Build from source

Requires Go 1.26 (the exact version pinned in `go.mod`).

```
make build
make install
```

`make install` puts the binary at `~/.local/bin/stoat`. Make sure
`~/.local/bin` is on your `PATH`.

### Install from a release

Download the tarball for your architecture from the
[releases page](https://github.com/novusedge/stoat/releases) (e.g.
`stoat_vX.Y.Z_linux_amd64.tar.gz`), verify it against the accompanying
`checksums.txt`, then extract and move the binary onto your `PATH`:

```
sha256sum -c --ignore-missing checksums.txt
tar xzf stoat_vX.Y.Z_linux_amd64.tar.gz
install -Dm755 stoat ~/.local/bin/stoat
```

### First run

On first run, stoat creates its data root at `~/.stoat` (override the
location with `$STOAT_HOME`), with two subdirectories: `isos/` and
`recipes/`. Nothing else is created until you add a VM.

### Getting an ISO

In the "new vm" form, the ISO picker's first entry is
`⤓ download latest Alpine…`, which fetches and checksum-verifies the latest
Alpine ISO straight into `~/.stoat/isos/`. Alternatively, drop any ISO file
into `~/.stoat/isos/` yourself and it will show up in the picker too.

### Development

There is both a `justfile` and a `Makefile` with the same targets — use
whichever you have. `just` additionally offers `just check` (what the
pre-commit hook runs) and `just run -- <args>`.

```
just hooks     # or: make hooks
just test      # or: make test
just check     # gofmt -l, go vet, go build
```

`hooks` points git at `.githooks` to enable the pre-commit and commit-msg
hooks.

> **Note:** if your shell aliases `make` to `just`, `make build`/`make
> install`/etc. will fail with `error: No justfile found` — that alias now
> resolves to the justfile, which has the same targets, so it will work.
> To reach the real Makefile explicitly, use `command make ...`.

## Quick start

```
make install
stoat
```

## Keys

### List

| Key | Action |
|-----|--------|
| `↵` | start / stop |
| `→` / `l` | details |
| `s` | ssh in (only works on a running VM) |
| `n` | new vm |
| `d` then `y` | delete (any other key cancels; prompts `y/N`) |
| `q` / `ctrl+c` | quit |

### Detail

| Key | Action |
|-----|--------|
| `e` | edit `vm.toml` in `$EDITOR` |
| `i` | toggle installed (disk VMs only) |
| `s` | ssh in (only works on a running VM) |
| `esc` | back to list |
| `ctrl+c` | quit |

## Live vs. disk VMs

VMs are either **live** or **disk**.

- **Live** VMs are diskless and disposable: they boot straight from the ISO
  and discard all state on stop. Nothing persists between runs.
- **Disk** VMs keep a qcow2 that survives restarts. Run `setup-alpine` once
  in the QEMU window, then press `i` on the detail screen to mark the VM
  installed.

## Phase 1 limitation

Live VMs boot without an apkovl, so they come up at a console login with
**no ssh access** — `s` will not work on them. This is expected in phase 1;
provisioning live VMs over ssh arrives in phase 2. Disk VMs are fully
usable today: run `setup-alpine` in the QEMU window, then press `i`.

## Data layout

Data lives in `~/.stoat` (override with `$STOAT_HOME`):

```
~/.stoat/
  isos/        downloaded ISO images
  recipes/     provisioning recipes
  <vm-name>/   one directory per VM, with a hand-editable vm.toml
```
