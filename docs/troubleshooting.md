# Troubleshooting

Symptom-first. Find the error text you're seeing and jump to it.

## No QEMU window appears

A VM with `display = "vnc"` in its `vm.toml`, or one started on a host with no
graphical session, puts its screen on a VNC socket instead of a window. Every
other VM opens a real QEMU window by default (`internal/qemu/args.go`), whether
it is live, cloud, or an installed or uninstalled disk VM.

**Fix:** ask stoat where the screen went. Both `stoat up` and `stoat get` print
it, with a command for a VNC viewer that is actually installed on your machine:

```
$ stoat get alpinedisk
...
display: no qemu window; the screen is on /home/user/.stoat/alpinedisk/vnc.sock
  attach with: gvncviewer /home/user/.stoat/alpinedisk/vnc.sock
```

The TUI's detail screen shows the same socket and command. If no viewer is
installed, stoat names what to install rather than printing a command that
would fail. With only `socat`, the command bridges the socket to loopback and
any VNC client connects to `127.0.0.1:5900`.

Set `display = "vnc"` (or cycle it with the `d` key in the TUI) to keep a VM
headless on purpose: `-display none` binds a VNC server at launch instead of a
window, which stays reachable even if the guest locks up or loses its network.

## `OpenGL is not supported by display backend 'gtk'`

```
stoat: up: qemu failed to start: qemu-system-x86_64: OpenGL is not supported by display backend 'gtk'
```

`gl=on` is the first option QEMU rejects when it can't open a window at all;
mesa and GPU drivers aren't involved. stoat starts a VM with
`-display gtk,gl=on` by default.
With the same window and no `gl=on`, the same host says `gtk initialization
failed` instead.

If stoat prints this, it found a graphical session on the host and QEMU still
could not use it: usually a QEMU or GTK build without working GL. The message
now says so, and names the way past it:

```
run with STOAT_GRAPHICAL=0 to put the screen on this VM's VNC socket instead
```

## Installing a disk VM on a machine with no screen

The install runs unattended, so a headless host needs no interaction: stoat
bakes a `setup-alpine` answerfile into the boot overlay, the guest installs
itself on first boot and reboots, and stoat notices the install. You wait, you
do not drive anything.

On a host with no graphical session there is no window, and QEMU does not fall
back: `-display gtk` exits 1. So stoat puts the console on the VM's VNC socket
instead, for watching the install if you want to:

```
$ stoat up alpinedisk
starting alpinedisk...
alpinedisk started (ssh :2200)
display: no usable graphical session on this host, so the install console is
  on VNC instead; attach to watch it
display: no qemu window; the screen is on /home/user/.stoat/alpinedisk/vnc.sock
  attach with: gvncviewer /home/user/.stoat/alpinedisk/vnc.sock
```

That socket is a unix socket on the server, so reaching it from your laptop is
three steps: run stoat's own `socat` bridge on the server to republish it on
`127.0.0.1:5900`, forward that port over ssh (`ssh -L 5900:127.0.0.1:5900
server`), and point any VNC client at `127.0.0.1:5900` on the laptop.

### Overriding the detection

stoat looks at `DISPLAY`, at `WAYLAND_DISPLAY`, and at
`$XDG_RUNTIME_DIR/wayland-0`, which is where GTK finds a Wayland session when
`WAYLAND_DISPLAY` is not set. Guessing about display servers goes wrong on
setups nobody anticipated, so `STOAT_GRAPHICAL` overrides the answer in both
directions, for every command and for the TUI:

| value | effect |
| --- | --- |
| `STOAT_GRAPHICAL=0` | never open a window; every VM's screen goes to VNC |
| `STOAT_GRAPHICAL=1` | open the window; use this if stoat did not recognize your session |
| unset | detect (the default) |

`STOAT_GRAPHICAL=0` is also the answer to the OpenGL error above, where a
session exists but QEMU cannot draw on it.

## `ssh not reachable on port N after 1m30s`

```
<name>: ssh not reachable on port <N> after 1m30s
```

(`internal/sshx/sshx.go`'s `Wait`, with `WaitTimeout` set to 90 seconds.)

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

`i` on the detail screen still toggles `installed` by hand, for when that
guess goes wrong in either direction: an install that died halfway leaves
enough bytes on the disk to look finished (the threshold is `installedBytes`
in `internal/qemu/run.go`), and `i` is how you get the ISO back.

## Applying recipes to a disk VM fails with `Permission denied (publickey,...)`

An Alpine disk VM gets the same apkovl a live one does
(`internal/apkovl/apkovl.go`) while it is still uninstalled, precisely so
`setup-alpine`'s `setup-disk -m sys` (which copies the *running* system onto
the target) carries `/root/.ssh/authorized_keys` across to the installed
system. A VM installed before that behaviour existed has no such key.

**Fix:** at the guest console, paste your stoat public key
(`~/.stoat/id_stoat.pub`) into the installed system's
`/root/.ssh/authorized_keys`. Or reinstall: the install now carries the key
over on its own.

## A binary on `/mnt/host` won't run: confusing "not found"

You built a binary on the host, it shows up fine on the shared
`/mnt/host` (backed by QEMU's read-only `-virtfs` export with
`security_model=mapped-xattr`, `internal/qemu/args.go`), but running it inside
the guest fails with something
like:

```
sh: ./mybinary: not found
```

even though the file is right there and executable. This is almost always a
libc mismatch, not a missing file: your host binary is dynamically linked
against **glibc**, and stock Alpine is a **musl** system with no
`/lib64/ld-linux-x86-64.so.2`, the ELF interpreter path baked into a
glibc-linked binary. The kernel can't find that interpreter to load the
binary, and the resulting error reads exactly like "file not found" even
though the file is there.

**Fixes**, in order of how little they touch:

- Build statically instead: `CGO_ENABLED=0 go build` (for Go binaries) removes
  the dynamic libc dependency entirely.
- Add a glibc compatibility shim on the guest: `apk add gcompat`.
- Sidestep it: use a glibc-based cloud VM (Ubuntu/Debian/Fedora via the
  cloudinit backend) instead of an Alpine guest.

## `/dev/kvm not usable`

```
/dev/kvm not usable: <err> (are you in the kvm group?)
```

(`internal/qemu/run.go`, from an `os.OpenFile("/dev/kvm", os.O_RDWR, 0)`
probe.) stoat always passes `-enable-kvm` (there's no software-emulation
fallback), so it needs read/write access to `/dev/kvm` before it will start
anything.

**Fix:** add yourself to the group that owns `/dev/kvm` (commonly `kvm`) and
start a new login session (`newgrp kvm`, or log out and back in) so the group
membership actually takes effect. Confirm with `ls -l /dev/kvm` and `groups`.

## A live VM lost everything after a reboot

Live mode works this way by design. A live VM's root filesystem is a
`tmpfs`/`overlay` mount that only exists in RAM for that boot
(`internal/apkovl/apkovl.go`'s doc comment, and the same detection every
bundled recipe uses: `awk '$2 == "/" { print $3 }' /proc/mounts` reporting
`tmpfs` or `overlay`). Every package you installed, every file you edited,
is gone the moment the VM restarts: only the apkovl itself (rebuilt fresh on
every `Start`, per `internal/qemu/run.go`) survives, and it can't carry
installed packages.

The host still keeps the VM's applied recipe records. A matching recipe with
`run = "once"` can therefore be skipped after a live restart even though its
package or files disappeared with the guest. For work that must run on every
boot, copy the recipe into a project or local scope, set `run = "always"` in
its manifest, and select that copy for the VM. Existing bundled once recipes
may report a successful no-op after a restart.

**If you want state to survive a reboot, live mode is the wrong mode.** Create
a **disk** VM instead (install the OS once, then it persists like a normal
machine) or a **cloud** VM (a prebuilt image where cloud-init's `packages:`/
`runcmd:` apply once at first boot and then stick).

## A VM shows as `broken` in the list

```
<name>: broken vm.toml, cannot start (d to delete)
```

A VM's directory exists under the data root but its `vm.toml` doesn't parse
(`internal/config/config.go`'s `ListBroken`). stoat still shows it, rather
than hiding a directory it can't fully understand, so you know it's there and
can act on it, but it can't be started, edited, or have recipes applied in
that state.
Its reserved ssh port stays held (`FreePort` checks broken VMs' raw `vm.toml`
port fields too), so a broken VM won't silently let a new VM reuse its port
out from under it.

**Fix:** either hand-edit the `vm.toml` in that VM's directory to fix whatever
made it unparseable, or delete the directory: press `d` on it in the list and
confirm.

## `stoat rm` refuses to delete a running VM

```
stoat: rm: <name> is running; stop it first
```

(`internal/cli/cli.go`'s `runRM`, gated on `qemu.Running(v)`.) `rm` won't
delete a VM out from under a live QEMU process.

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
