# stoat documentation

stoat is a terminal UI for running local QEMU virtual machines. It is
Alpine-first, and it manages each VM through a small `vm.toml` file instead of
a system daemon or a virtualization stack like libvirt.

stoat is for developers who want a disposable Linux VM (or a handful of
persistent ones) on their own machine, driven entirely from the terminal.
stoat needs no libvirt, no background daemon, and no database, just a single
Go binary that shells out to `qemu-system-x86_64`, `qemu-img`, and `ssh`, and
a data root of plain files under `~/.stoat` (or `$STOAT_HOME`).

Use the [installation guide](getting-started/installation.md) to prepare the
host, then follow [Your first VM](getting-started/first-vm.md). Alpine live is
the shortest path to a working shell. Alpine disk VMs install unattended and
retain their disk; cloud VMs use a first-boot cloud-init seed.

For repeatable development environments, commit a `stoat.toml` and its
`stoat.lock`, then use the [project workflow](guides/project-workflow.md).
For an agent client, use the [MCP workflow](guides/mcp-workflow.md).

The [concepts](SUMMARY.md#concepts) explain storage, access, modes,
networking, project shares, and provisioning. The [reference](SUMMARY.md)
contains the CLI, JSON, TUI, guest, project, and recipe details.

stoat is pre-1.0 and single-user: it assumes it is the only process managing
its data root and does not manage remote VMs or provide multi-tenant
isolation. Cloud-image and installed-disk paths are implemented, but Alpine
live has received the most exercise. Start with
[Troubleshooting](troubleshooting.md) when a command reports an error.
