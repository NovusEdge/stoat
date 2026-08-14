# CLI Reference

`stoat` with a subcommand runs non-interactively (`internal/cli`); with none, it launches the [TUI](tui.md) instead. No subcommand contains logic of its own: each is a thin wrapper over the same internal packages the TUI uses, so the two interfaces can't drift apart.

```
usage: stoat <command> [flags]
```

**Single-dash long flags do not work.** `--image` is required on `create`; `-image` is parsed as a shorthand cluster (`-i -m -a -g -e`) and rejected as an unknown flag. Always use two dashes for a named flag; a single dash is only for the one-letter forms (`-q`, `-y`, `-n`, `-h`).

## Global flags

- **`--json`** turns on machine output: one JSON object per line on stdout, errors included, never prose. It implies `--quiet` and never prompts (so `rm` without `-y` fails instead of asking). It is recognized anywhere in argv before kong ever parses flags, with one exception: for `exec`, only before the VM name, so `stoat exec work ls --json` still sends `--json` to the guest. This file only covers the human-facing CLI; the JSON shapes themselves are in [json.md](json.md).
- **`-q`, `--quiet`, `--no-interactive`** are three names for one flag, present on every subcommand. Where it has an effect, it suppresses "in-progress" chatter (`starting work...`, `provisioning work...`, ...); final results and all errors print regardless.
- **`-h`, `--help`** prints the command's usage and flags and exits 0. `stoat help` (the subcommand) prints the same top-level text `stoat --help` does.
- **`-v`, `--version`** prints `stoat <version>` and exits 0. It is matched as the **first argument only**, before any parsing (`cmd/stoat/main.go`), which has two consequences worth knowing: `stoat -v --json` prints plain text and ignores `--json`, and `stoat --json --version` is a usage error because `-v` is no longer first. **Scripts and machine consumers should use the `version` subcommand**, which behaves normally under `--json`.

## Subcommands

| Command | Synopsis | Exit codes |
|---|---|---|
| [`ls`](#stoat-ls) | List VMs, one line per VM | 0, 1 |
| [`get`](#stoat-get-name) | Show one VM's details | 0, 1 |
| [`create`](#stoat-create-name---imageimage) | Create a VM without starting it | 0, 1, 2 |
| [`update`](#stoat-update-name) | Change a stopped VM; only the flags you pass change | 0, 1, 2 |
| [`up`](#stoat-up-name) | Start a VM | 0, 1 |
| [`down`](#stoat-down-name) | Stop a VM (graceful) | 0, 1 |
| [`wait`](#stoat-wait-name) | Block until a VM reaches a state | 0, 1, 2 |
| [`rm`](#stoat-rm-name--y) | Delete a VM | 0, 1 |
| [`clone`](#stoat-clone-source-name) | Copy a VM: overlay disk, fresh ssh port, no forwards | 0, 1 |
| [`exec`](#stoat-exec-name-command-) | Run a command in a VM, verbatim | 0-255, see below |
| [`ssh`](#stoat-ssh-name) | ssh into a VM, replacing this process | 0, 1, 2 |
| [`ssh-command`](#stoat-ssh-command-name) | Print the ssh argv instead of running it | 0, 1 |
| [`cp`](#stoat-cp-source-dest) | Copy a file in or out; one side is `<vm>:<path>` | 0, 1, 2 |
| [`forward`](#stoat-forward-name-pairs) | Show, set or clear host:guest port forwards | 0, 1, 2 |
| [`images`](#stoat-images) | List catalog and local images | 0, 1 |
| [`pull`](#stoat-pull-id) | Download a catalog image | 0, 1 |
| [`snapshot`](#stoat-snapshot-name-tag) | List, save, restore or delete a snapshot | 0, 1, 2 |
| [`prune`](#stoat-prune) | Report, or with `--apply` remove, stale files | 0, 1 |
| [`apply`](#stoat-apply-name) | Run the VM's recipes, streaming output | 0, 1 |
| [`provision`](#stoat-provision-name) | Run recipes, streaming output to stdout | 0, 1 |
| [`recipes`](#stoat-recipes) | List recipes, optionally only applicable ones | 0, 1 |
| [`check-recipes`](#stoat-check-recipes-names---osos) | Report why a recipe would not apply | 0, 1, 2 |
| [`recipe list`](#stoat-recipe-list) | List installed recipes and where they live | 0, 1 |
| [`recipe new`](#stoat-recipe-new-name) | Scaffold a recipe in the recipes directory | 0, 1 |
| [`logs`](#stoat-logs-name--n-n) | Tail a VM's log, or stoat's own | 0, 1 |
| [`doctor`](#stoat-doctor) | Check host prerequisites | 0, 1 |
| [`version`](#stoat-version) | Print the stoat version | 0 |
| [`help`](#stoat-help) | Show the usage message | 0 |

Anything not on this list, a missing VM name, or extra arguments is a **usage error** (exit 2), printed to stderr together with the full usage text.

## `stoat ls`

Lists every VM directory under the data root, plus any directory whose `vm.toml` failed to parse (shown with a `broken` state and a one-line reason). Broken VMs are real entries, not hidden.

```
$ stoat ls
NAME            MODE  STATE    CPUS RAM    SSH
work            live  running  4    4096   2222
scratch         disk  stopped  2    2048   2223
oldvm           -     broken   -    -      -    unexpected token near line 4
```

The `STATE` column is colored (green `running`, red `broken`) when [color is enabled](#scripting). `-q`/`--quiet` is accepted but has no effect on `ls`'s output.

**Exit codes:** 0 on success; 1 if the data root can't be read.

## `stoat get <name>`

Prints one VM's fields as `key: value` lines: name, os, mode, backend, state, cpus, ram, disk, share, ssh port, ssh user, recipes, forwards, display, plus an `error:` line when the VM is broken.

```
$ stoat get work
name: work
os: alpine
mode: live
backend: apkovl
state: running
cpus: 4
ram: 4096
disk: 8G
share: /home/user/Projects
ssh port: 2222
ssh user: root
recipes: xfce
forwards: 8080:80
display: a qemu window
```

`display` is the only line here that is not a `vm.toml` field. See [`stoat up`](#stoat-up-name) for what it means and why the answer changes. It is omitted entirely for a broken VM, whose `vm.toml` supplies neither of the facts the answer depends on.

**Exit codes:** 0 on success; 1 if the VM can't be loaded.

## `stoat create <name> --image=IMAGE`

Creates a VM without starting it. `--image` is the only required flag; everything else has a sensible default or is inferred from the image.

```
$ stoat create work --image alpine --recipes xfce
created work (alpine, live, ssh port 2222)
start it with: stoat up work
```

Flags: `--image` (required; catalog id or a path to your own image), `--os`, `--backend` (override what a bring-your-own image's filename would otherwise infer), `--mode` (`live` or `disk`; only meaningful for the alpine iso, every other image has one mode), `--ram` (MB), `--cpus`, `--disk` (absolute size, e.g. `8G`), `--share` (host directory to expose), `--console-password` (`random` generates one), `--recipes` (comma-separated or repeated), `--allow-exec` (default true; `--allow-exec=false` opts this VM out of `exec`/`copy_to`/`copy_from`, enforced by the MCP server rather than stoat itself).

**Exit codes:** 0 on success; 1 if creation fails (e.g. the image isn't downloaded yet: run `stoat pull` or download it from the TUI's image picker first); 2 if `--image` is missing.

## `stoat update <name>`

Changes a stopped VM. **Only the flags you actually pass are changed**; an omitted flag leaves that field alone, and an explicitly empty one clears it. This is the single most consequential behaviour in the command:

```
$ stoat update work --ram 8192
updated work: [ram]
```

`--ram` and nothing else changed: cpus, disk, share, ssh-port and recipes are untouched, even though the flags for them exist. To clear the share instead of changing it:

```
$ stoat update work --share ""
updated work: [share]
```

`work`'s share is now unset. Compare to `stoat update work` with no flags at all, which is a usage error (there is nothing to change), not a no-op.

Flags: `--ram`, `--cpus`, `--ssh-port`, `--disk` (grow-only), `--share` (empty clears it), `--recipes` (empty clears it; replaces the whole list, it does not add to it).

Most fields are read by qemu only at start, so a change to a *running* VM is saved to `vm.toml` but doesn't take effect until the VM is next started; `update` says so:

```
$ stoat update work --cpus 8
updated work: [cpus]
work is running; this takes effect at next start
```

**Exit codes:** 0 on success; 1 if the VM can't be loaded or the update itself fails; 2 if no flags were given.

## `stoat up <name>`

Starts a VM.

```
$ stoat up work
starting work...
work started (ssh :2222)
display: no qemu window; the screen is on /home/user/.stoat/work/vnc.sock
  attach with: gvncviewer /home/user/.stoat/work/vnc.sock
```

`-q`/`--quiet`/`--no-interactive` suppresses the `starting <name>...` line; the final result line always prints.

### Where the screen is

A VM gets a real QEMU window by default, on a host with a graphical session. Set `display = "vnc"` in `vm.toml` (or cycle it with the `d` key in the TUI) to keep a VM headless instead. QEMU then starts with `-display none` and a VNC server bound to a unix socket in the VM's directory; `-display none` cannot be undone on a running QEMU, so binding VNC at launch keeps a misbehaving guest recoverable.

```
$ stoat up work
starting work...
work started (ssh :2222)
display: no qemu window; the screen is on /home/user/.stoat/work/vnc.sock
  attach with: gvncviewer /home/user/.stoat/work/vnc.sock
```

The attach command names a viewer that is actually installed on your machine:

- `gvncviewer <socket>` opens the socket directly, when `gvncviewer` is present.
- Otherwise `socat TCP-LISTEN:5900,bind=127.0.0.1,reuseaddr,fork UNIX-CONNECT:<socket>` republishes it on loopback, and any VNC client connects to `127.0.0.1:5900`.
- If neither is installed, `up` says so and names them rather than printing a command that would fail.

### On a host with no graphical session

`-display gtk` does not degrade when there is no display server: QEMU exits 1. So every VM's screen goes to VNC there instead, and `up` says why before it says where:

```
$ stoat up alpinedisk
starting alpinedisk...
alpinedisk started (ssh :2200)
display: no usable graphical session on this host, so the screen
  is on VNC instead; attach to watch it
display: no qemu window; the screen is on /home/user/.stoat/alpinedisk/vnc.sock
  attach with: gvncviewer /home/user/.stoat/alpinedisk/vnc.sock
```

The check looks at `DISPLAY`, `WAYLAND_DISPLAY` and `$XDG_RUNTIME_DIR/wayland-0` (GTK's own fallback when `WAYLAND_DISPLAY` is unset). `STOAT_GRAPHICAL=0` forces the VNC path and `STOAT_GRAPHICAL=1` forces the window, for every command and for the TUI. Use `0` when a host has a session QEMU cannot draw on, which surfaces as `OpenGL is not supported by display backend 'gtk'`; see [troubleshooting](../troubleshooting.md).

**Exit codes:** 0 on success; 1 if the VM can't be loaded or fails to start (including a broken VM, which is refused before the `starting...` line is even printed).

## `stoat down <name>`

Stops a VM gracefully. Refuses if the VM isn't already running.

```
$ stoat down work
stopping work...
work stopped
```

`-q` suppresses the `stopping <name>...` line only.

**Exit codes:** 0 on success; 1 if the VM can't be loaded, is broken, isn't running, or fails to stop.

## `stoat wait <name>`

Blocks until the VM reaches a state, or the timeout expires.

```
$ stoat wait work --until reachable
work reached reachable (1240ms)
```

`--until` is one of `reachable` (sshd answering on the VM's forwarded port, default), `applied` (the most recent recipe run finished), or `stopped` (qemu no longer running). `--timeout` (default `2m`) is a Go duration (`30s`, `5m`).

A request that cannot ever be satisfied fails immediately rather than waiting out the timeout: `--until applied` on a VM with no recipes configured, or `--until reachable` on a VM that isn't running.

**Exit codes:** 0 if the state was reached; 1 if the timeout expires or the state can't be reached at all; 2 if `--timeout` is zero or negative.

## `stoat rm <name> [-y]`

Deletes a VM's directory. Refuses outright if it's currently running.

```
$ stoat rm scratch
delete VM scratch? [y/N] y
scratch deleted
```

Without `-y`, confirmation is required: interactively it prompts on stdout and reads a line from stdin (anything other than a `y`/`Y` aborts); in `-q`/`--quiet`/`--no-interactive` mode there is no prompt to answer, so it refuses outright instead of guessing. Under `--json` the same rule applies for the same reason: nothing reads stdin, so `-y` is required or the command fails with `confirmation_required`. `-y` skips the prompt in every mode.

**Exit codes:** 0 if deleted; 1 if the VM can't be loaded, is running, the confirmation is declined or aborted, `-y` was needed but not given, or the delete itself fails. Note that declining the confirmation prompt is exit 1, not 0: a script checking `$?` sees "delete didn't happen" as a failure either way, whether the VM was running or the user just said no.

## `stoat clone <source> <name>`

Copies a VM: a fresh overlay disk, a fresh ssh port, but **not** the source's port forwards.

```
$ stoat clone work work-2
cloned work to work-2 (ssh :2223)
port forwards were not copied; set them with: stoat forward work-2 ...
```

Refuses a running source.

**Exit codes:** 0 on success; 1 if the source can't be loaded, is running, or the clone itself fails.

## `stoat exec <name> <command>...`

Runs a command in a VM over ssh and, without `--json`, **exits with the guest's own exit status** (the same convention `ssh` itself uses). `stoat exec vm make test && deploy` means what it looks like.

Everything after the VM name is sent to the guest **verbatim**, including tokens that look like stoat flags:

```
$ stoat exec work ls -la /etc
$ stoat exec work echo --json
```

The second line prints `--json` in the guest, it does not turn on stoat's JSON mode; `--json` only has that effect when it appears *before* the VM name (`stoat --json exec work ...` or `stoat exec --json work ...`). An optional leading `--` before the command is accepted and dropped, but not required.

Because the guest's status and stoat's own share one exit-code range, a guest command exiting 2 is indistinguishable, on the shell, from a stoat usage error; that trade is accepted rather than remapped, the same one `ssh` itself makes. stoat's own failures (no such VM, not running) still exit 1 and print to stderr, distinguishable from guest output.

**Exit codes:** without `--json`, the guest's own exit status (0-255) on success, or 1 for a stoat-side failure before the command ever ran. With `--json`, the process always exits 0 once the guest command ran at all; the guest's real status is in the JSON `exit_code` field instead, so a consumer parsing the line can always tell a guest failure from a stoat one. `--json` still exits 1 if stoat itself failed to run the command.

## `stoat ssh <name>`

Looks up `ssh` on `$PATH` and **replaces the current process** with it via `syscall.Exec` (the same as running `ssh` directly): signals and the terminal behave exactly as a bare `ssh` invocation, and stoat leaves no supervisor process behind.

```
$ stoat ssh work
```

`-q` is accepted but has no effect (there is no chatter to suppress before the process is replaced). `--json` is refused outright: `syscall.Exec` destroys the process image, so there is no "after" in which to write a result line; the error message points at `stoat --json exec` for a single command, or `ssh_port`/`ssh_user` from `stoat --json ls` to build your own connection.

**Exit codes:** 0 is not actually observed on success: the process image is gone. 1 if the VM can't be loaded, `ssh` isn't found on `$PATH`, or `exec` itself fails to launch. 2 under `--json`, always (see above).

## `stoat ssh-command <name>`

Prints the exact `ssh` argv stoat would run, instead of running it: for scripts that want to build their own ssh invocation or hand it to another tool.

```
$ stoat ssh-command work
ssh -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=5 -o BatchMode=yes -i /home/user/.stoat/id_stoat root@127.0.0.1
```

**Exit codes:** 0 on success; 1 if the VM can't be loaded.

## `stoat cp <source> <dest>`

Copies a file in or out of a VM. Direction is **inferred** from which side carries a `<vm>:` prefix, the same spelling `scp`/`docker cp` use; exactly one side must have it.

```
$ stoat cp ./build.sh work:/root/build.sh
copied ./build.sh to work:/root/build.sh
$ stoat cp work:/var/log/app.log ./app.log
copied work:/var/log/app.log to ./app.log
```

There is also an explicit-flag form, `--vm`, `--direction` (`to` or `from`), `--local` and `--remote`, alongside the positional one rather than replacing it: a host path that legitimately contains a colon is ambiguous in the `<vm>:<path>` spelling, and a machine caller (the MCP server) needs an unambiguous one. The two forms are mutually exclusive; giving both, or giving some but not all of the flag form's four flags, is a usage error.

```
$ stoat cp --vm work --direction to --local ./build.sh --remote /root/build.sh
```

Under `--json`, the result's `local` field is always the **resolved absolute path**, even when a relative or `~`-prefixed path was given, so a caller can verify what actually ran (see [json.md](json.md)).

**Exit codes:** 0 on success; 1 if the copy fails; 2 for a malformed invocation (neither or both sides carry a `<vm>:` prefix, both forms given, or an incomplete flag set).

## `stoat forward <name> [pairs...]`

Shows, sets or clears a VM's host:guest port forwards. **With no pairs and no `--clear`, it prints the current forwards; it does not clear them.**

```
$ stoat forward work
8080:80
```

```
$ stoat forward work 8080:80 2222:22
8080:80
2222:22
```

Pairs are `HOST:GUEST`, host port first (the ordering `docker` and `ssh -L` both use). Setting a forward on a running VM saves it but does not make it live until the VM is next started, and `forward` says so. `--clear` removes every forward from the VM and takes no port pairs.

```
$ stoat forward work --clear
cleared work's port forwards
```

**Exit codes:** 0 on success; 1 if the VM can't be loaded or the forward fails; 2 if `--clear` is combined with port pairs, or a pair isn't a valid `HOST:GUEST` number pair.

## `stoat images`

Lists what stoat can build a VM from: the catalog, plus anything else already downloaded under `isos/`.

```
$ stoat images
ID               OS        VARIANT     SIZE       STATE
ubuntu-24.04     ubuntu    24.04 LTS   595.2MiB   downloaded
debian-13        debian    13 (trixie) 326.2MiB   downloaded
fedora-cloud     fedora    44          556.3MiB   downloaded
```

SIZE is exact for a downloaded image and approximate (prefixed `~`) for a catalog entry not yet pulled. A bring-your-own image already on disk (no catalog id) shows its filename as ID and state `byo`.

**Exit codes:** 0 on success; 1 if the catalog or local image list can't be read.

## `stoat pull <id>`

Downloads a catalog image, printing live progress.

```
$ stoat pull ubuntu-24.04
ubuntu-24.04  100%  595.2MiB / 595.2MiB
ubuntu-24.04 downloaded
```

If the image has no published checksum to verify against, the final line says so (`UNVERIFIED (no published checksum)`) rather than staying silent about it, since this image is about to be booted. `^C` cancels a download in progress rather than leaving it running in the background.

**Exit codes:** 0 on success; 1 if the download fails.

## `stoat snapshot <name> [tag]`

Lists, saves, restores or deletes a disk snapshot. There is no separate "list" flag: **a bare `<tag>` saves**, and `--restore`/`--delete` act on an existing `<tag>` instead. `--restore` and `--delete` are mutually exclusive.

```
$ stoat snapshot work
TAG                      SIZE       CREATED              RAM
before-upgrade           1.2GiB     2026-07-30 10:04:00  yes
```

```
$ stoat snapshot work before-upgrade
saved before-upgrade
$ stoat snapshot work before-upgrade --restore
work restored to before-upgrade
$ stoat snapshot work before-upgrade --delete
deleted before-upgrade
```

Snapshots need a disk to snapshot: a `live` VM (no persistent disk) has nothing to snapshot and the command refuses with an explanatory error rather than a generic failure.

**Exit codes:** 0 on success; 1 if the VM can't be loaded, has no disk to snapshot, the tag doesn't exist for `--restore`/`--delete`, or the operation itself fails; 2 if `--restore` or `--delete` is given without a tag, or both are given together.

## `stoat prune`

Reports stale files stoat can clean up: broken VM directories, partial downloads, orphaned images. **Dry-run by default; `--apply` is what actually deletes.**

```
$ stoat prune
broken vm: /home/user/.stoat/vms/oldvm
partial download: /home/user/.stoat/isos/ubuntu-24.04.iso.part

(dry run: nothing was deleted; re-run with --apply)
```

```
$ stoat prune --apply
broken vm: /home/user/.stoat/vms/oldvm
partial download: /home/user/.stoat/isos/ubuntu-24.04.iso.part
```

`--broken` also considers VMs whose `vm.toml` won't parse for removal; `--images` also considers downloaded images no VM refers to. Without either, `prune` only reports partial downloads by default (broken VMs and orphaned images need to be asked for explicitly). Printing an identical list for the dry run and the real run is deliberate: the two are meant to be readable as the same thing, one with the deletions actually applied.

**Exit codes:** 0 on success, including "nothing to prune"; 1 if pruning fails.

## `stoat apply <name>`

Runs a VM's own recipes over ssh and streams `apply.log` to stdout as it's written.

```
$ stoat apply work
applying recipes to work...
=== recipe xfce ===
Unpacking libx11-data...
...
work: recipes applied
```

`--only` restricts the run to a subset of the VM's own recipe list (comma-separated or repeated), instead of applying all of them.

**Exit codes:** 0 on success; 1 if the VM can't be loaded, the run fails, or (for a cloud-mode VM) recipes were already applied at boot rather than by this command.

## `stoat provision <name>`

Runs the VM's recipes over ssh, streaming `last-provision.log` to stdout as it's written (polled every 150ms), the same log the TUI's detail screen tails, so there is no separate provisioning path to keep in sync.

```
$ stoat provision work
provisioning work...
=== recipe xfce ===
Unpacking libx11-data...
...
work provisioned
```

A **cloud-mode VM short-circuits**: cloud-init applies its recipes once, automatically, at first boot, baked into the seed when the VM's overlay was created, there is nothing left for ssh-based provisioning to do, and piping a cloud recipe (`#cloud-config` YAML, not a shell script) into `sh -s` would just fail. Instead it prints an explanatory line and exits 0 without touching ssh:

```
$ stoat provision cloudvm
cloudvm is a cloud VM: recipes are applied automatically via cloud-init at first boot; recreate the VM to change them.
```

`-q` suppresses the `provisioning <name>...` line only; the streamed log and the final line still print.

**Exit codes:** 0 on success (including the cloud short-circuit); 1 if the VM can't be loaded or the provision run itself fails.

## `stoat recipes`

Lists recipes, optionally filtered to ones applicable to a guest OS and/or backend.

```
$ stoat recipes --os alpine --backend apkovl
NAME                           DESCRIPTION
devtools                       git, a compiler, an editor and basic fetch tools
docker                         Docker engine and the compose plugin
tailscale                      Tailscale daemon, installed and started (join manually)
xfce                           XFCE desktop with autologin startx on tty1
```

`--os` alone means "what that OS gets" (its own backend is inferred); `--backend` alone means "every OS on that backend"; both together is the exact filter; neither is the full catalog.

**Exit codes:** 0 on success; 1 if the recipe list can't be built.

## `stoat check-recipes <names>... --os=OS`

Reports, for each named recipe, why it would **not** apply to the given OS/backend; an empty result means every one of them would.

```
$ stoat check-recipes docker xfce --os debian --backend cloudinit
docker: docker is not offered to debian/cloudinit
$ stoat check-recipes xfce --os alpine --backend apkovl
all applicable
```

`--os` is required; `--backend` narrows further.

**Exit codes:** 0 on success, whether or not any recipe turned out inapplicable (an inapplicable recipe is a valid answer, not a failure); 1 if the check itself fails; 2 if no recipe names are given.

## `stoat recipe list`

Lists recipes installed under stoat's recipes directory and prints where that directory is.

```
$ stoat recipe list
/home/user/.stoat/recipes
  devtools
  docker
  tailscale
  xfce
```

**Exit codes:** 0 on success; 1 if the directory can't be read.

## `stoat recipe new <name>`

Scaffolds a new recipe file in the recipes directory and prints its path.

```
$ stoat recipe new mytool --os alpine
/home/user/.stoat/recipes/mytool.alpine.sh
edit it, then pick it in the new-vm form for a matching vm
```

`--backend cloudinit` scaffolds a cloud-init fragment instead of a shell script. `-q` suppresses the trailing hint line.

**Exit codes:** 0 on success; 1 if the recipe can't be created (e.g. the name is already taken).

## `stoat logs [name] [-n N]`

With a VM name, tails that VM's own log (`--which console` for the qemu console, the default, or `--which apply` for its apply log). With no name, tails stoat's own log file.

```
$ stoat logs work -n 20
...
$ stoat logs -n 20
...
```

`-n` sets how many lines from the end to print (default 50; `0` or negative prints the whole file).

**Exit codes:** 0 on success (including an empty log, which prints nothing); 1 if the log can't be opened or read.

## `stoat doctor`

Checks host prerequisites: qemu/KVM, `qemu-img`, `ssh`, `xorriso` and `/dev/kvm`, the same set the installer's own checklist runs, so `stoat doctor` and `just setup` can't disagree about whether the host is ready.

```
$ stoat doctor
ok
```

or, with problems:

```
$ stoat doctor
FAIL: /dev/kvm not accessible
      try: sudo usermod -aG kvm $USER
FAIL: ssh not found in PATH
```

A failed check that has a known fix prints a `try:` line under it.

**Exit codes:** without `--json`, 0 if every check passes, 1 if any fails. With `--json`, always 0: `doctor` succeeded at checking, and an unhealthy host is the answer it was asked for, not a failure to produce one; the JSON `healthy` field carries the result instead.

## `stoat version`

Prints the build's version string as `stoat <version>`. Equivalent to the top-level `-v` / `--version` flag (`stoat -v`, `stoat --version`), which is handled before any subcommand dispatch and produces identical output.

**Exit codes:** always 0.

## `stoat help`

Prints the full usage message (subcommands, global flags, exit codes) to stdout. The same text is printed to stderr, alongside the specific error, whenever a usage error occurs.

**Exit codes:** always 0.

## Exit codes

| Code | Meaning | Examples |
|---|---|---|
| `0` | Success | VM started/stopped, provisioned, deleted; `doctor` found nothing wrong |
| `1` | Runtime failure | Unknown VM name, VM already stopped for `down`, VM running for `rm`, ssh unreachable during provision, `doctor` found an issue, `rm` confirmation declined |
| `2` | Usage error | Unknown subcommand, missing/extra arguments, an unparseable flag, `update` given no flags, `check-recipes` given no names |

A usage error (2) always prints both the specific complaint and the full usage text to stderr; a runtime failure (1) prints only `stoat: <command>: <error>` to stderr. `exec` is the one command whose exit code, without `--json`, is neither: it is the guest's own status, 0-255 (see [`stoat exec`](#stoat-exec-name-command-)).

## Scripting

- **`-q`, `--quiet`, `--no-interactive`** are three names for the same flag, present on every subcommand. Where it has an effect, it suppresses the "in-progress" chatter (`starting work...`, `provisioning work...`, ...); final results and all errors print regardless of this flag.
- **`rm`** also treats `--no-interactive`/`-q`/`--json` as "there is no one to answer a confirmation prompt": without `-y` it refuses rather than blocking on stdin.
- **`--json`** is the machine-readable mode: one JSON object per line on stdout, errors included, implying `--quiet` and never prompting. See [json.md](json.md) for the wire format.
- **`NO_COLOR`** (any non-empty value) disables ANSI color in `ls`'s output.
- Color is also **disabled automatically whenever stdout is not a terminal** (checked via `os.ModeCharDevice`), so piping `stoat ls` into `awk`, `grep`, or a file never carries escape codes even without setting `NO_COLOR`. Only `ls`'s `STATE` column is ever colored.
