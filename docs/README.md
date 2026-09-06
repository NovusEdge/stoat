# Stoat

Run Linux virtual machines on your computer, from a terminal or an AI agent.

A virtual machine (VM) runs its own operating system inside your computer.
Use one to try Linux, test software, or give an agent an environment to work
in. Stoat manages local QEMU VMs through a command-line interface (CLI), an
interactive terminal interface (TUI), and the Model Context Protocol (MCP).

![Stoat's terminal interface lists two running VMs and one stopped VM.](https://raw.githubusercontent.com/NovusEdge/stoat/main/assets/tui-list.png)

[Watch the 23-second demo](https://github.com/NovusEdge/stoat/blob/main/assets/demo.mp4)
to see the terminal interface and an Alpine XFCE desktop running in QEMU.

## Start here

Choose a guide for your task:

| I want to… | Guide |
|---|---|
| Install Stoat on my Linux computer | [Installation](getting-started/installation.md) |
| Create a VM and open a shell | [Your first VM](getting-started/first-vm.md) |
| Connect an AI agent | [MCP workflow](guides/mcp-workflow.md) |
| Use Stoat from scripts or a shell-based agent | [CLI reference](reference/cli.md) and [JSON output](reference/json.md) |
| Keep a repeatable environment in a repository | [Project workflow](guides/project-workflow.md) |

## Choose an environment

Alpine live VMs provide disposable sessions. Changes inside the guest are
lost when it stops. Installed disk VMs and cloud-image VMs retain their
disks between runs. See [Modes and backends](concepts/modes-and-backends.md)
before choosing where to keep work you need to save.

[Recipes](recipes/overview.md) install software and configure a guest. They
can set up a desktop, development tools, or other software after the VM starts.

## Requirements and limits

Stoat runs on Linux with KVM, QEMU, and OpenSSH. Cloud images also require
`xorriso`. The [installation guide](getting-started/installation.md) covers
host packages and permissions; `stoat doctor` checks your setup.

Stoat is pre-1.0 and single-user. It manages VMs on the local machine and
assumes it is the only process managing its data root. It does not provide
multi-tenant isolation. Read [Access and auth](concepts/access-and-auth.md)
before granting an agent guest access or sharing host files.

If a command fails, start with [Troubleshooting](troubleshooting.md).
The source and releases are on [GitHub](https://github.com/NovusEdge/stoat).
