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

| Requirement | Why Stoat needs it |
|---|---|
| Linux with KVM | Stoat opens `/dev/kvm` directly and runs QEMU with hardware acceleration |
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

**On a host with no graphical session** (a server or an SSH session without
forwarding), Stoat sends each VM's screen to a VNC socket and prints connection
instructions. It detects the session from `DISPLAY`, `WAYLAND_DISPLAY`, and
`$XDG_RUNTIME_DIR/wayland-0`.

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

Download the tarball for your architecture from the releases page. For
example, the AMD64 archive is named `stoat_vX.Y.Z_linux_amd64.tar.gz`.
Download the accompanying `checksums.txt`, then verify and install the binary:

```sh
sha256sum -c --ignore-missing checksums.txt
tar xzf stoat_vX.Y.Z_linux_amd64.tar.gz
install -Dm755 stoat ~/.local/bin/stoat
```

## Install via the Nix flake

The repository includes a `flake.nix` with a default package and development
shell:

```sh
nix build          # ./result/bin/stoat
nix run            # build and run in one step
nix develop        # a dev shell with go, just, qemu and openssh
```

The development shell provides Go, Just, QEMU, and OpenSSH. Cloud VMs also
require `xorriso` on `PATH`. The flake pins the Go dependencies with
`vendorHash`.

### Enable flakes

If Nix reports `experimental Nix feature 'nix-command' is disabled`, enable
`nix-command` and `flakes` for that invocation:

```sh
nix --extra-experimental-features 'nix-command flakes' build
```

To enable them for your user account, add the setting to
`~/.config/nix/nix.conf`:

```sh
mkdir -p ~/.config/nix
echo 'experimental-features = nix-command flakes' >> ~/.config/nix/nix.conf
```

### Start the Nix daemon

If Nix reports `creating directory "/nix/store": Permission denied`, the Nix
daemon or store may not be initialized. On a systemd-based multi-user
installation, start the daemon socket:

```sh
sudo systemctl enable --now nix-daemon.socket
```

Verify the connection with:

```sh
nix store info
```

For a daemon installation, the output should include `Store URL: daemon`.

### Update `vendorHash`

A change to `go.mod` or `go.sum` can invalidate `vendorHash`. When this occurs,
Nix reports a hash mismatch and prints the required value:

```
error: hash mismatch in fixed-output derivation ...
     specified: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
        got:    sha256-hyDEjT73s7rOJ/zRxYBF2ZUOADSqLmKCM1eJJJRF8Y4=
```

Copy the exact `got:` value into `vendorHash` in `flake.nix`.

Nix builds from Git-tracked content. It ignores untracked files and includes
tracked, uncommitted changes. To build the committed state explicitly, run:

```sh
nix build "git+file://$PWD"
```

The flake does not wrap the `stoat` binary with QEMU or OpenSSH. Stoat uses
the `qemu-system-x86_64` and `ssh` commands on `PATH`. Graphical use therefore
requires a QEMU build with GTK and OpenGL support. Run `nix develop` to use the
development-shell versions.

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
  id_stoat, id_stoat.pub   the SSH keypair Stoat uses to reach your VMs
  logs/stoat.log           Stoat's own log
```

Nothing else is created until you add a VM: each one gets its own directory under `~/.stoat` once you create it.

Next: [Your First VM](first-vm.md).
