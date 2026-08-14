# stoat

stoat is a terminal UI for running local QEMU virtual machines. It is
Alpine-first, and it manages each VM through a small `vm.toml` file instead of
a system daemon or a virtualization stack like libvirt.

stoat is for developers who want a disposable Linux VM (or a handful of
persistent ones) on their own machine, driven entirely from the terminal.
stoat needs no libvirt, no background daemon, and no database, just a single
Go binary that shells out to `qemu-system-x86_64`, `qemu-img`, and `ssh`, and
a data root of plain files under `~/.stoat` (or `$STOAT_HOME`).

New here? Start with [Installation](getting-started/installation.md), then
[Your first VM](getting-started/first-vm.md).

## Status

stoat is pre-1.0 and single-user: it assumes it's the only thing managing its
data root, and there's no remote access or multi-tenant story. The Alpine
live-boot path (`apkovl` provisioning, no install to disk) is the most
exercised mode, so that's what to reach for first. Cloud-image provisioning
(`cloud-init` seeds) and installed disk VMs work but have seen less mileage.
Expect rough edges, and check [Troubleshooting](troubleshooting.md) if
something doesn't behave as documented.
