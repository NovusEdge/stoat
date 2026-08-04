# stoat

A terminal UI and CLI for running local QEMU VMs on Linux. No libvirt, no daemon, one binary.

## What it does

- Boots an Alpine live VM that comes up already networked and `ssh`-reachable: an apkovl overlay is baked into the boot, so there's no `setup-alpine` step to get in.
- Also runs Ubuntu, Debian, Fedora, and Arch cloud images, provisioned via cloud-init on first boot.
- Persistent disk VMs for anything else: install once with the guest's own installer, then boot straight to it.
- Ships a few ready-made recipes (XFCE, Docker, dev tools, Tailscale) to run post-boot over ssh or bake into a cloud-init seed. `stoat recipe new` scaffolds your own.
- QEMU processes are tracked by pidfile, not supervised: `stoat` can exit and the VM keeps running.
- A TUI for interactive use, and a scriptable CLI (`ls`, `up`, `down`, `ssh`, `provision`, `rm`, `recipe`, `logs`, `doctor`) covering the same operations for scripts and automation.

## What it looks like

The list screen, with one running VM, one stopped, and one whose `vm.toml` failed to parse (captured with `tmux capture-pane` against a throwaway `STOAT_HOME`):

```
                                 ███████╗████████╗ ██████╗  █████╗ ████████╗
                                 ██╔════╝╚══██╔══╝██╔═══██╗██╔══██╗╚══██╔══╝
                                 ███████╗   ██║   ██║   ██║███████║   ██║
                                 ╚════██║   ██║   ██║   ██║██╔══██║   ██║
                                 ███████║   ██║   ╚██████╔╝██║  ██║   ██║
                                 ╚══════╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝   ╚═╝

                     ╭────────────────────────────────────────────────────────────────╮
                     │                                                                │
                     │  ❯ ○ alpine-live    live   2048M  2c  -                        │
                     │                                                                │
                     │    ○ ubuntu-dev     cloud  4096M  4c  -                        │
                     │                                                                │
                     │    ✗ old-vm         broken: toml: line 1: expected '.' or      │
                     │  '=', but got 'i' instead                                      │
                     │                                                                │
                     ╰────────────────────────────────────────────────────────────────╯

↵ start/stop • →/l details • s ssh • p provision • / search • n new • r recipes • d delete • q quit • ? help
```

The same VMs from `stoat ls`:

```
$ stoat ls
NAME            MODE  STATE    CPUS  RAM    SSH
alpine-live     live  stopped  2     2048   2200
ubuntu-dev      cloud stopped  4     4096   2201
old-vm          -     broken   -     -      -    toml: line 1: expected '.' or, but got 'i' instead
```

## Requirements

- Linux with KVM: `/dev/kvm` readable and writable by your user (member of the `kvm` group).
- `qemu-system-x86_64` and `qemu-img`. GTK+OpenGL support is needed only for the one VM that opens a window (`-display gtk,gl=on`, a disk VM mid-install); with no graphical session on the host, that console falls back to VNC and the VM still installs.
- `ssh`.
- `xorriso` (package `libisoburn`), only if you use a cloud-init image (Ubuntu/Debian/Fedora/Arch). Not needed for Alpine live or disk VMs.
- Go 1.26 to build from source (the version pinned in `go.mod`).

## Install

```sh
just setup
```

Builds stoat, installs it to `~/.local/bin` (override with `$PREFIX`), offers to add that directory to your `PATH`, and reports which of `qemu-system-x86_64`, `qemu-img`, `ssh`, `xorriso`, and `/dev/kvm` still need attention, with the exact install command for your distro.

For a non-interactive install (CI, scripts):

```sh
just build && just install    # or: make build && make install
```

`install` puts the binary at `$PREFIX/stoat` (default `~/.local/bin/stoat`); make sure that's on your `PATH`.

Release tarballs and the Nix flake are covered in [docs/getting-started/installation.md](docs/getting-started/installation.md).

## Quick start

```
stoat
```

1. Press `n` for a new VM.
2. Pick an image and press SPACE to download it (not enter: enter tries to create the VM and tells you to download first).
3. Press ENTER to create the VM.
4. Back on the list, press ENTER to start it.
5. If you checked any recipes, stoat waits for sshd and then offers to run
   them: `test1 is up, run xfce now? y/N`. Press `y`, or decline and press
   `p` whenever you like.
6. Press `s` to ssh in.

See [docs/getting-started/first-vm.md](docs/getting-started/first-vm.md) for the walkthrough with screenshots and troubleshooting.

## Commands

| Command | Purpose |
|---|---|
| `stoat` | launch the interactive TUI |
| `stoat ls` | list VMs, one line per VM |
| `stoat up <name>` | start a VM |
| `stoat down <name>` | stop a VM (graceful) |
| `stoat ssh <name>` | ssh into a VM, replacing this process |
| `stoat provision <name>` | run recipes, streaming output to stdout |
| `stoat rm <name> [-y]` | delete a VM; refuses while running, confirms unless `-y` |
| `stoat recipe list` | list installed recipes and where they live |
| `stoat recipe new <name> [--os alpine] [--backend cloudinit]` | scaffold a recipe in the recipes directory |
| `stoat logs [-n N]` | tail the stoat log (default 50 lines) |
| `stoat doctor` | check host prerequisites |
| `stoat version` | print the stoat version |

Global flags: `-q`/`--quiet`/`--no-interactive` suppress progress chatter (results and errors still print). Exit codes: 0 success, 1 runtime failure, 2 usage error. Full reference: [docs/reference/cli.md](docs/reference/cli.md); TUI keys: [docs/reference/tui.md](docs/reference/tui.md).

## How it works

Every VM is one of three modes:

- **live**: diskless, boots straight from an Alpine ISO, discards all state on stop. Provisioned by an **apkovl** overlay built fresh at every start.
- **disk**: a qcow2 that survives restarts. You run the guest's own installer once, then flip it to "installed". Provisioned by pushing recipes over **ssh**.
- **cloud**: a CoW overlay over a shared Ubuntu/Debian/Fedora/Arch base image. Provisioned by a **cloud-init** seed baked in once, at first boot only.

Each VM is a directory under `~/.stoat` (override with `$STOAT_HOME`) holding a hand-editable `vm.toml` plus whatever state its mode keeps, nothing else is shared between VMs. See [docs/concepts/modes-and-backends.md](docs/concepts/modes-and-backends.md) and [docs/concepts/data-root.md](docs/concepts/data-root.md).

Recipes (the scripts and cloud-init fragments that install XFCE, Docker, etc.) are covered in [docs/recipes/overview.md](docs/recipes/overview.md), including how to write your own.

## Status

Pre-1.0 and single-user: stoat assumes it's the only thing managing its `~/.stoat`, and offers no sandboxing beyond what QEMU/KVM already give a guest. The Alpine live path is the most exercised mode; the cloud-init backends and disk-mode installs are newer. `vm.toml`'s shape and the CLI's flags may still change before 1.0.

## License

AGPL-3.0-or-later. Copyright (c) 2026 Aliasgar Khimani (NovusEdge).

Free to clone, run, and modify, including by automated agents and harnesses. Any distributed or network-hosted derivative must publish its source under the same license, so stoat cannot be forked into a proprietary product.
