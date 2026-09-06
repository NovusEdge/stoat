# Installation

Stoat runs on Linux and uses QEMU and OpenSSH to manage local VMs. Use the
[guided setup](#guided-setup-with-just) to build and install it, or choose a
[release tarball](#install-from-a-release-tarball) or the
[Nix flake](#install-via-the-nix-flake).

## Guided setup with Just

Install Git, Go 1.26 or newer, and [Just](https://just.systems/man/en/).
Then run the installer:

1. Clone the repository:

   ```sh
   git clone https://github.com/NovusEdge/stoat.git
   ```

2. Open the repository directory:

   ```sh
   cd stoat
   ```

3. Start guided setup:

   ```sh
   just setup
   ```

Setup checks host dependencies, builds Stoat, and lets you choose the install
directory. The default is `~/.local/bin`. It creates the data directories
under `~/.stoat` and offers to add the install directory to your shell's
`PATH`. The Just target also enables this clone's Git hooks.

Setup reports missing host packages and suggests commands to install them.
Run those commands yourself; setup does not install system packages or change
KVM permissions. See [Prerequisites](#prerequisites).

If setup changes your shell configuration, open a new terminal. Then check
that your host is ready:

```sh
stoat doctor
```

### Setup without an interactive terminal

- From the repository root, run:

  ```sh
  just setup-headless
  ```

This installs to `~/.local/bin` without prompts. To choose a different
binary directory, set `PREFIX`:

```sh
PREFIX="$HOME/bin" just setup-headless
```

Headless setup reports a missing `PATH` entry but does not edit your shell
configuration. Host-check warnings do not stop installation, so run
`stoat doctor` afterward to confirm VM prerequisites.

### Run the installer without Just

The installer is a Go program in the repository.

- From the clone's root, run:

  ```sh
  go run ./cmd/installer
  ```

For headless setup, add `--no-tty`. Running it directly does not enable Git
hooks; that step belongs to the Just targets.

## Prerequisites

| Requirement | Why stoat needs it |
|---|---|
| Linux with KVM | stoat opens `/dev/kvm` directly and runs QEMU with hardware acceleration |
| `qemu-system-x86_64` and `qemu-img` | run and create VMs |
| `ssh` (an OpenSSH client) | connect into a running VM, and provision it |
| `xorriso` | used when you create Ubuntu/Debian/Fedora/Arch/Alpine cloud VMs: it builds the cloud-init seed image |
| Go 1.26 | **only** if you're building from source (the exact version pinned in `go.mod`) |

### QEMU and SSH

On Arch:

```sh
sudo pacman -S --needed qemu-full openssh
```

(`qemu-desktop` also works and is smaller. Choose a QEMU package that provides
`qemu-system-x86_64`, `qemu-img`, and the GTK/OpenGL display backend described
below.)

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

A VM opens a real QEMU window by default on a host with a graphical session.
Stoat passes `-display gtk,gl=on`, so the QEMU binary on your `PATH` must have
GTK and OpenGL support. Set `display = "vnc"` in `vm.toml` to keep a VM
headless with its screen on a VNC socket.

**On a host with no graphical session** (a server, an ssh session with no forwarding) stoat does not ask for a window at all: every VM's screen goes to a VNC socket and stoat prints how to attach, so a disk VM can still be installed from another machine. It detects this from `DISPLAY`, `WAYLAND_DISPLAY` and `$XDG_RUNTIME_DIR/wayland-0`.

If a VM still fails to start with a display or GL error, your host has a session QEMU cannot draw on. Set `STOAT_GRAPHICAL=0` to take the window out of play; see [troubleshooting](../troubleshooting.md). No source edit and no rebuild.

### xorriso (cloud VMs)

Provisioning an Ubuntu, Debian, Fedora, Arch, or Alpine cloud image builds a small ISO9660 seed via `xorriso`. Alpine live and disk VMs do not need it. If it is missing, creating or starting a cloud VM fails with:

```
xorriso is required for cloud-init provisioning; install libisoburn
```

On Debian/Ubuntu the package is `xorriso`; on Arch it's `libisoburn` (which provides the `xorriso` binary).

## Build from source

Requires Go 1.26.

The repository has a `justfile` and a smaller `Makefile`. They share `build`,
`install`, `hooks`, and `test`; the `justfile` also provides `setup`, `check`,
`lint`, `e2e`, and other development targets. Use the `justfile` for the
[guided setup](#guided-setup-with-just), or build and install directly:

```sh
just build
just install
```

or

```sh
make build
make install
```

`just install` builds the binary and copies it to `~/.local/bin/stoat` by
default. Set `PREFIX` to change that destination. The Makefile's `install`
target always uses `~/.local/bin`. Make sure the destination is on your
`PATH`.

Other useful targets: `just test` runs the test suite, `just check` runs the
same `gofmt`/`go vet`/`go build` checks as the pre-commit hook, and `just hooks`
points Git at `.githooks` to enable it.

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

The development shell provides the build tools and QEMU/OpenSSH packages. Add
`xorriso` to the shell or host `PATH` when you use cloud VMs. The flake includes
a pinned `vendorHash` for the current `go.mod` and
`go.sum`. A dependency change requires updating that hash from the value Nix
prints in its mismatch error.

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

This checks `qemu-system-x86_64`, `qemu-img`, `ssh`, `xorriso`, Git (optional),
and `/dev/kvm`. It prints failed checks and a suggested command for each. A
missing required check exits with status 1. On a ready host it prints:

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
