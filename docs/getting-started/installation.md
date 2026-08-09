# Installation

stoat is a single Go binary that shells out to `qemu-system-x86_64`, `qemu-img`, and `ssh`: there is no daemon, no libvirt, and nothing to configure before you build it. This page covers what your host needs, the ways to get the binary onto it, and how to confirm it's ready.

## Prerequisites

| Requirement | Why stoat needs it |
|---|---|
| Linux with KVM | stoat opens `/dev/kvm` directly and runs QEMU with hardware acceleration |
| `qemu-system-x86_64` and `qemu-img` | run and create VMs |
| `ssh` (an OpenSSH client) | connect into a running VM, and provision it |
| `xorriso` | **only** if you'll create Ubuntu/Debian/Fedora/Arch cloud VMs: it builds the cloud-init seed image |
| Go 1.26 | **only** if you're building from source (the exact version pinned in `go.mod`) |

### QEMU and SSH

On Arch:

```sh
sudo pacman -S --needed qemu-full openssh
```

(`qemu-desktop` also works and is smaller; either package provides `qemu-system-x86_64` and `qemu-img` with GTK/OpenGL display support, see below.)

On Debian/Ubuntu:

```sh
sudo apt install qemu-system-x86 qemu-utils openssh-client
```

### KVM access

`/dev/kvm` must be readable and writable by your user. Check its group:

```sh
ls -l /dev/kvm
```

The group should be `kvm`, and your user should be a member of it:

```sh
id -nG
```

If you're not in the `kvm` group, add yourself and then **log out and back in**: group membership is only picked up on new sessions:

```sh
sudo usermod -aG kvm "$USER"   # then log out and back in
```

### GPU/display

A VM opens a real QEMU window by default. That window is `-display gtk,gl=on`, so your QEMU build needs GTK and OpenGL support (the `qemu-full`/`qemu-desktop` Arch packages and the Debian/Ubuntu packages above provide this). Set `display = "vnc"` on a VM to keep it headless with its screen on a VNC socket instead.

**On a host with no graphical session** (a server, an ssh session with no forwarding) stoat does not ask for a window at all: every VM's screen goes to a VNC socket and stoat prints how to attach, so a disk VM can still be installed from another machine. It detects this from `DISPLAY`, `WAYLAND_DISPLAY` and `$XDG_RUNTIME_DIR/wayland-0`.

If a VM still fails to start with a display or GL error, your host has a session QEMU cannot draw on. Set `STOAT_GRAPHICAL=0` to take the window out of play; see [troubleshooting](../troubleshooting.md). No source edit and no rebuild.

### xorriso (cloud VMs only)

Provisioning an Ubuntu, Debian, Fedora, or Arch cloud image builds a small ISO9660 seed via `xorriso`. Alpine VMs (live or disk) never need it. If it's missing, creating or starting a cloud VM fails with:

```
xorriso is required for cloud-init provisioning; install libisoburn
```

On Debian/Ubuntu the package is `xorriso`; on Arch it's `libisoburn` (which provides the `xorriso` binary).

## Build from source

Requires Go 1.26.

There's both a `justfile` and a `Makefile` exposing the same targets, use whichever you have:

```sh
just build
just install
```

or

```sh
make build
make install
```

`install` builds the binary and copies it to `~/.local/bin/stoat` (with `just`, override the destination via `PREFIX`). Make sure `~/.local/bin` is on your `PATH`.

> If your shell aliases `make` to `just`, `make build`/`make install` still work: the alias resolves to the justfile, which has the same targets. To reach the real Makefile explicitly, use `command make ...`.

Other useful `just`/`make` targets: `just test` runs the test suite, `just check` runs the same `gofmt`/`go vet`/`go build` checks as the pre-commit hook, and `just hooks` points git at `.githooks` to enable it.

## Install from a release tarball

Download the tarball for your architecture from the releases page (e.g. `stoat_vX.Y.Z_linux_amd64.tar.gz`), verify it against the accompanying `checksums.txt`, then extract and move the binary onto your `PATH`:

```sh
sha256sum -c --ignore-missing checksums.txt
tar xzf stoat_vX.Y.Z_linux_amd64.tar.gz
install -Dm755 stoat ~/.local/bin/stoat
```

## Install via the Nix flake

The repo ships a `flake.nix` with a `packages.default` (built with `buildGoModule`, `subPackages = [ "cmd/stoat" ]`) and a `devShells.default` that provides `go`, `just`, `qemu`, and `openssh`, the same binaries `just doctor` checks for.

```sh
nix build          # ./result/bin/stoat
nix run            # build and run in one step
nix develop        # a dev shell with go, just, qemu and openssh
```

The `vendorHash` is pinned and the build works as-is, no hash dance required.

### If `nix build` fails before it starts building

Two things trip people up, and neither is stoat-specific. Both produced a confusing error for us:

**`error: experimental Nix feature 'nix-command' is disabled`**: flakes are still gated. Either pass the features per invocation:

```sh
nix --extra-experimental-features 'nix-command flakes' build
```

or enable them once, for your user only (no root needed):

```sh
mkdir -p ~/.config/nix
echo 'experimental-features = nix-command flakes' >> ~/.config/nix/nix.conf
```

**`error: creating directory "/nix/store": Permission denied`**: Nix is installed but its daemon has never run, so the store was never created. On a distro package (e.g. Arch's `nix`), finish the setup with:

```sh
sudo systemctl enable --now nix-daemon.socket
```

That is normally the only command needing root: the package already creates `/nix`, the `nixbld` build users and `build-users-group = nixbld` in `/etc/nix/nix.conf`, and the daemon socket has no group restriction, so you do not need to join a group. Check it took with `nix store info`: it should report `Store URL: daemon`.

### When the hash goes stale

`vendorHash` is derived from `go.mod` and `go.sum`, so **any** dependency change invalidates it, including a bare `go mod tidy` that only drops an unused indirect requirement. When that happens the build fails with a mismatch that prints the correct value:

```
error: hash mismatch in fixed-output derivation ...
     specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
        got:    sha256-hyDEjT73s7rOJ/zRxYBF2ZUOADSqLmKCM1eJJJRF8Y4=
```

Paste the `got:` value into `vendorHash` in `flake.nix`. Do not guess at it.

Note also that Nix builds from **git-tracked** content: untracked files are invisible to the build, and a dirty tree builds your uncommitted changes (with a "Git tree is dirty" warning). To build exactly what a clone would get, build the committed state explicitly:

```sh
nix build "git+file://$PWD"
```

Also note: the flake deliberately does **not** wrap the `stoat` binary to force nixpkgs' `qemu`/`openssh` onto its `PATH`. stoat shells out to whatever `qemu-system-x86_64`/`ssh` it finds, so you still need a QEMU build with GTK+OpenGL support on your `PATH` regardless of how you installed stoat itself. `nix develop` gives you a shell with matching versions for development.

## Verify with `stoat doctor`

Once the binary is installed, check that your host is ready:

```sh
stoat doctor
```

This checks that `qemu-system-x86_64` is on your `PATH`, that `/dev/kvm` is usable, and that `ssh` is on your `PATH`. On success it prints:

```
ok
```

On failure it prints one `FAIL:` line per problem found (for example `FAIL: /dev/kvm not usable: ... (are you in the kvm group?)`) and exits non-zero. Fix whatever it names and run it again.

## First run

The first time you run `stoat` (either the TUI with no arguments, or any CLI subcommand other than `version`/`help`), it creates its data root at `~/.stoat` (override the location with `$STOAT_HOME`) and populates it:

```
~/.stoat/
  isos/                    empty, downloaded/BYO images land here
  recipes/                 bundled provisioning recipes, copied in (local edits survive future upgrades)
  id_stoat, id_stoat.pub   the SSH keypair stoat uses to reach your VMs
  logs/stoat.log           stoat's own log
```

Nothing else is created until you add a VM: each one gets its own directory under `~/.stoat` once you create it.

Next: [Your First VM](first-vm.md).
