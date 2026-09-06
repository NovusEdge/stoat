# Troubleshooting

Find the reported symptom or error text below, then follow its recovery steps.

## No QEMU window appears

A VM with `display = "vnc"` in its `vm.toml`, or one started on a host with no
graphical session, puts its screen on a VNC socket instead of a window. Every
other VM opens a QEMU window by default. This behavior applies to live, cloud,
and disk VMs.

**Fix:** ask Stoat where the screen went. Both `stoat up` and `stoat get` print
it, with a command for a VNC viewer that is actually installed on your machine:

```
$ stoat get alpinedisk
...
display: no qemu window; the screen is on /home/user/.stoat/alpinedisk/vnc.sock
  attach with: gvncviewer /home/user/.stoat/alpinedisk/vnc.sock
```

The TUI's detail screen shows the same socket and command. If no viewer is
installed, Stoat names what to install rather than printing a command that
would fail. With only `socat`, the command bridges the socket to loopback and
any VNC client connects to `127.0.0.1:5900`.

Set `display = "vnc"` (or cycle it with the `d` key in the TUI) to keep a VM
headless on purpose: `-display none` binds a VNC server at launch instead of a
window, which stays reachable even if the guest locks up or loses its network.

## `OpenGL is not supported by display backend 'gtk'`

```
stoat: up: qemu failed to start: qemu-system-x86_64: OpenGL is not supported by display backend 'gtk'
```

Stoat starts a graphical VM with `-display gtk,gl=on`. This error means that
QEMU cannot use the requested GTK and OpenGL display.

Use the VNC display when the host session or QEMU build does not support this
configuration:

```
run with STOAT_GRAPHICAL=0 to put the screen on this VM's VNC socket instead
```

## Installing a disk VM on a machine with no screen

The Alpine disk installation is unattended. Stoat adds a `setup-alpine`
answer file to the boot overlay. The guest installs itself on first boot and
then restarts from the disk.

On a host without a graphical session, Stoat places the console on the VM's
VNC socket. You can use this socket to monitor the installation:

```
$ stoat up alpinedisk
starting alpinedisk...
alpinedisk started (ssh :2200)
display: no usable graphical session on this host, so the install console is
  on VNC instead; attach to watch it
display: no qemu window; the screen is on /home/user/.stoat/alpinedisk/vnc.sock
  attach with: gvncviewer /home/user/.stoat/alpinedisk/vnc.sock
```

The VNC socket is a Unix socket on the server. To access it from another
machine:

1. Run the `socat` bridge printed by Stoat on the server. This publishes the
   socket on `127.0.0.1:5900`.
2. Forward that port over SSH:

   ```sh
   ssh -L 5900:127.0.0.1:5900 server
   ```

3. Connect a local VNC client to `127.0.0.1:5900`.

### Overriding the detection

Stoat checks `DISPLAY`, `WAYLAND_DISPLAY`, and
`$XDG_RUNTIME_DIR/wayland-0`, which is where GTK finds a Wayland session when
`WAYLAND_DISPLAY` is not set. `STOAT_GRAPHICAL` overrides this detection for
every command and for the TUI:

| value | effect |
| --- | --- |
| `STOAT_GRAPHICAL=0` | never open a window; every VM's screen goes to VNC |
| `STOAT_GRAPHICAL=1` | open the window; use this if Stoat did not recognize your session |
| unset | detect (the default) |

`STOAT_GRAPHICAL=0` is also the answer to the OpenGL error above, where a
session exists but QEMU cannot draw on it.

## `ssh not reachable on port N after 1m30s`

```
<name>: ssh not reachable on port <N> after 1m30s
```

**If this happens while applying recipes to a disk VM:** the VM is still
installing itself off the ISO. Its guest OS is not on the disk yet, so there
are no recipes to apply, and `sshd` answering means the *installer's* sshd is
up, not the system you are building.

**Fix:** wait. The unattended `setup-alpine` finishes, reboots into the disk,
and the next start notices the OS, marks the VM installed and drops the ISO from
the boot order. Pressing `p` before that is refused outright, rather than making
you wait out the full timeout to find out:

```
<name>: installing itself; wait for it to finish and reboot, then stoat notices the install and offers to apply recipes
```

Press `i` on the detail screen if automatic installation detection is wrong.
This changes the `installed` state and controls whether the installer ISO is
used on the next start.

## Applying recipes to a disk VM fails with `Permission denied (publickey,...)`

Before installation, an Alpine disk VM receives an apkovl containing Stoat's
SSH key. `setup-alpine` copies the running system to the target disk, including
`/root/.ssh/authorized_keys`. A VM installed before this behavior was added
does not contain that key.

**Fix:** at the guest console, paste your Stoat public key
(`~/.stoat/id_stoat.pub`) into the installed system's
`/root/.ssh/authorized_keys`. Or reinstall: the install now carries the key
over on its own.

## A binary on `/mnt/host` reports `not found`

A host binary is visible on the read-only `/mnt/host` share, but running it in
the guest fails with an error such as:

```
sh: ./mybinary: not found
```

The file exists and is executable. The likely cause is a C library mismatch:
the host binary is dynamically linked against **glibc**, and stock Alpine is a
**musl** system without
`/lib64/ld-linux-x86-64.so.2`, the ELF interpreter path baked into a
glibc-linked binary. The kernel cannot load the missing interpreter and reports
the binary as not found.

Use one of these fixes:

- Build statically instead: `CGO_ENABLED=0 go build` (for Go binaries) removes
  the dynamic libc dependency entirely.
- Add a glibc compatibility shim on the guest: `apk add gcompat`.
- Sidestep it: use a glibc-based cloud VM (Ubuntu/Debian/Fedora via the
  cloudinit backend) instead of an Alpine guest.

## `xorriso is required for cloud-init provisioning; install libisoburn`

Stoat uses `xorriso` to build the cloud-init seed ISO. The binary is an
external dependency and must be available on `PATH`.

**Fix:** install the package that provides `xorriso`, on most distros that's
`libisoburn` (the message names it directly), e.g. `apk add xorriso` on
Alpine or your distro's equivalent of `libisoburn`/`xorriso`.

## `/dev/kvm not usable`

```
/dev/kvm not usable: <err> (are you in the kvm group?)
```

Stoat requires KVM and does not fall back to software emulation. The current
user must have read and write access to `/dev/kvm`.

**Fix:** add yourself to the group that owns `/dev/kvm` (commonly `kvm`) and
start a new login session (`newgrp kvm`, or log out and back in) so the group
membership actually takes effect. Confirm with `ls -l /dev/kvm` and `groups`.

## A live VM lost everything after a reboot

Live mode stores the root filesystem in a temporary `tmpfs` or `overlay`
mount. Packages and file changes disappear when the VM stops or restarts.
Stoat rebuilds the apkovl at each start; the apkovl does not preserve installed
packages.

The host still keeps the VM's applied recipe records. A matching recipe with
`run = "once"` can therefore be skipped after a live restart even though its
package or files disappeared with the guest. For work that must run on every
boot, copy the recipe into a project or local scope, set `run = "always"` in
its manifest, and select that copy for the VM. Existing bundled once recipes
may report a successful no-op after a restart.

Use a **disk** or **cloud** VM when guest state must survive a restart. A disk
VM installs the OS on persistent storage. A cloud VM starts from a persistent
image and applies its cloud-init configuration during first boot.

## A VM shows as `broken` in the list

```
<name>: broken vm.toml, cannot start (d to delete)
```

A broken VM has a directory under the data root, but its `vm.toml` file cannot
be parsed. Stoat shows the VM in the list, but cannot start, edit, or apply
recipes to it. Stoat also attempts to reserve the SSH port recorded in the
broken file so that a new VM does not reuse it.

**Fix:** either hand-edit the `vm.toml` in that VM's directory to fix whatever
made it unparseable, or delete the directory: press `d` on it in the list and
confirm.

## `stoat rm` refuses to delete a running VM

```
stoat: rm: <name> is running; stop it first
```

`stoat rm` does not delete a VM while its QEMU process is running.

**Fix:** stop the VM first (`stoat down <name>`, or `d` in the TUI once it's
stopped), then `rm` it.

## `stoat up` says the project declaration is immutable

Project scope reconciles an existing VM with `stoat.toml`. CPU, memory,
recipes, parameters, shares, and `agent_access` are mutable. The image and
disk size identify the VM's backing storage and cannot be changed in place.

**Fix:** if the image or disk declaration changed intentionally, stop and
remove that VM, then run `stoat up <key>` to create it again. Removing a VM
deletes its directory and persistent disk. A project command without a VM
name uses declaration order; use the key or global name when repairing one
entry.

## `stoat up` says `no VM ... in stoat.toml or ~/.stoat/vms`

Project scope is active only when `stoat.toml` exists in the current
directory. Stoat does not walk up to a parent directory. A bare VM argument
must be a declaration key, a declared `name`, or a global VM that exists in
the data root.

**Fix:** change to the directory containing the intended `stoat.toml`, use the
declaration key, or create a global VM with `stoat create --global ...`.

## `stoat recipe lock` says the lock is out of date

`stoat.toml` declares project recipe refs. `stoat.lock` records the commit to
use, and `.stoat/recipes/` is the local checkout. Editing `[recipes]` without
locking leaves the declaration unpinned.

**Fix:** run the following from the project directory:

```sh
stoat recipe lock
stoat recipe sync
```

Commit `stoat.toml` and `stoat.lock`. Keep `.stoat/` ignored. `recipe lock`
resolves refs but does not populate the cache; `recipe sync` makes the cache
match the lock. If the configured index has no matching name, pass a Git URL
for a repository containing `recipe.toml` or configure an index that publishes
the recipe.

## `stoat apply` says `not running` or `recipe not applicable`

`apply` sends the VM's selected recipe scripts over SSH. The VM must be
running, and the recipe must be in that VM's own recipe list and applicable to
its guest OS and backend. `apply --dry-run` computes the plan without SSH and
does not require a running VM.

**Fix:** start the VM, then inspect the plan:

```sh
stoat up <name> --no-apply
stoat apply <name> --dry-run
stoat apply <name>
```

Use `--only <recipe>` only for a recipe already listed on the VM. A recipe can
run again when its script changed or its manifest declares `run = "always"`;
`run = "once"` skips a matching applied version, and `run = "manual"` needs
an explicit `--only` selection.

## `stoat wait --healthy` times out

`wait --healthy` first waits for SSH, then checks every applied recipe that
declares a health command. A recipe without a health command contributes no
verdict. A failed check includes the recipe name and its last diagnostic line
when available.

**Fix:** inspect the apply log and VM state:

```sh
stoat logs <name> --which apply -n 200
stoat get <name>
stoat wait <name> --until reachable
```

Run the failing health command yourself with `stoat exec` only when the VM's
agent access policy and your workflow permit it. The CLI timeout is a Go
duration and defaults to two minutes; the MCP `wait` tool uses
`timeout_seconds` and caps it at 600 seconds.

## An MCP client cannot find `stoat` or project VMs

MCP clients launch the binary recorded by their configuration entry. The
entry also records its working directory, which determines project scope.

**Fix:** reinstall the entry from the intended directory and inspect it:

```sh
stoat mcp install claude-code
stoat mcp doctor
```

Use `stoat mcp install claude-code --project` when Claude Code should read a
project-local `.mcp.json`. The server uses stdio by default. For HTTP, use
`stoat mcp serve --http 127.0.0.1:7777`; non-loopback addresses are refused
because the server has no authentication.

If a tool reports an access refusal, raise `agent_access` with the CLI or TUI.
The MCP `update` tool can lower a VM's level but cannot raise it. `observe` is
needed for guest reads, `manage` for writes and recipe application, and `exec`
for command and job tools.
