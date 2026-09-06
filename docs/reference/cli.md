# CLI reference

Run `stoat` with a subcommand for non-interactive use. Run it without a
subcommand to open the [TUI](tui.md). Both interfaces call the same core VM
operations.

```
usage: stoat <command> [flags]
```

Use two dashes for long flags, such as `--image`. A single dash is valid only
for the documented one-letter forms: `-q`, `-y`, `-n`, and `-h`. For example,
`-image` is invalid.

## Global flags

- **`--json`** selects machine output for a named VM operation. Stdout contains
  one JSON object per line, including errors, and contains no prose. The flag
  implies `--quiet` and disables prompts. Project fan-out has a known
  exception: no-name `up`, `down`, and `apply` can print progress prose before
  the final JSON result. Use a named VM command when stdout must contain only
  JSON. For `exec`, place `--json` before the VM name; a later occurrence is
  passed to the guest command. See [JSON output](json.md) for the payloads.
- **`-q`, `--quiet`, `--no-interactive`** name the same flag. When supported by
  a command, it suppresses progress messages. Final results and errors are
  still printed.
- **`-h`, `--help`** prints the command's usage and flags and exits 0. `stoat help` (the subcommand) prints the same top-level text `stoat --help` does.
- **`-v`, `--version`** prints `stoat <version>` and exits 0 only when it is the
  first argument. `stoat -v --json` therefore prints plain text, while
  `stoat --json --version` is a usage error. Scripts should use the `version`
  subcommand, which supports `--json` normally.

## Project scope

A `stoat.toml` in the current directory activates project scope. `up`,
`down`, `apply`, `wait` and `rm` act on every declared VM, in declaration
order, when given no VM argument. A bare VM argument resolves against
`stoat.toml` first, then against a global VM name. See
[project-file.md](project-file.md).

## Subcommands

| Command | Synopsis | Exit codes |
|---|---|---|
| [`init`](#stoat-init---name-n) | Write a `stoat.toml` for this directory | 0, 1 |
| [`status`](#stoat-status) | One line per declared VM: state, health, drift | 0, 1 |
| [`ls`](#stoat-ls) | List VMs, one line per VM | 0, 1 |
| [`get`](#stoat-get-name) | Show one VM's details | 0, 1 |
| [`create`](#stoat-create-name---imageimage) | Create a VM without starting it | 0, 1, 2 |
| [`update`](#stoat-update-name) | Change a stopped VM; only the flags you pass change | 0, 1, 2 |
| [`up`](#stoat-up-name) | Start a VM | 0, 1 |
| [`down`](#stoat-down-name) | Stop a VM (graceful) | 0, 1 |
| [`wait`](#stoat-wait-name) | Block until a VM reaches a state | 0, 1, 2 |
| [`rm`](#stoat-rm-name--y) | Delete a VM | 0, 1 |
| [`clone`](#stoat-clone-source-name) | Copy a VM: overlay disk, fresh ssh port, no forwards | 0, 1 |
| [`exec`](#stoat-exec-name-command) | Run a command in a VM, verbatim | 0-255, see below |
| [`ssh`](#stoat-ssh-name) | ssh into a VM, replacing this process | 0, 1, 2 |
| [`ssh-command`](#stoat-ssh-command-name) | Print the ssh argv instead of running it | 0, 1 |
| [`cp`](#stoat-cp-source-dest) | Copy a file in or out; one side is `<vm>:<path>` | 0, 1, 2 |
| [`forward`](#stoat-forward-name-pairs) | Show, set or clear host:guest port forwards | 0, 1, 2 |
| [`images`](#stoat-images) | List catalog and local images | 0, 1 |
| [`pull`](#stoat-pull-id) | Download a catalog image | 0, 1 |
| [`snapshot`](#stoat-snapshot-name-tag) | List, save, restore or delete a snapshot | 0, 1, 2 |
| [`prune`](#stoat-prune) | Report, or with `--apply` remove, stale files | 0, 1 |
| [`apply`](#stoat-apply-name) | Run the VM's recipes, streaming output | 0, 1 |
| [`recipes`](#stoat-recipes) | List recipes, optionally only applicable ones | 0, 1 |
| [`check-recipes`](#stoat-check-recipes-names---osos) | Report why a recipe would not apply | 0, 1, 2 |
| [`recipe list`](#stoat-recipe-list) | List installed recipes and where they live | 0, 1 |
| [`recipe show`](#stoat-recipe-show-name) | Show one recipe's parameter and output contract | 0, 1 |
| [`recipe new`](#stoat-recipe-new-name) | Scaffold a recipe in the recipes directory | 0, 1 |
| [`recipe search`](#stoat-recipe-search-term) | Search the curated remote recipe index | 0, 1 |
| [`recipe add`](#stoat-recipe-add-ref) | Add a remote recipe to the active scope | 0, 1 |
| [`recipe lock`](#stoat-recipe-lock---global) | Resolve project recipe refs to commits | 0, 1 |
| [`recipe sync`](#stoat-recipe-sync---global) | Synchronize a recipe cache to its lock | 0, 1 |
| [`recipe update`](#stoat-recipe-update-names---global) | Repin remote recipes to current refs | 0, 1 |
| [`recipe rm`](#stoat-recipe-rm-name--y) | Remove a remote recipe | 0, 1 |
| [`guest ls`](#stoat-guest-ls) | List loaded guest OS definitions | 0 |
| [`guest show`](#stoat-guest-show-name) | Print one guest's merged definition | 0, 1 |
| [`logs`](#stoat-logs-name--n-n) | Tail a VM's log, or stoat's own | 0, 1 |
| [`screenshot`](#stoat-screenshot-name--o-path) | Write the VM's screen to a PNG | 0, 1 |
| [`capabilities`](#stoat-capabilities-vm) | Report current agent capabilities | 0, 1 |
| [`doctor`](#stoat-doctor) | Check host prerequisites | 0, 1 |
| [`mcp`](mcp.md) | Serve MCP, or configure and inspect a client entry | 0, 1, 2 |
| [`version`](#stoat-version) | Print the stoat version | 0 |
| [`help`](#stoat-help) | Show the usage message | 0 |

An unknown command, missing VM name, or extra argument is a **usage error**
(exit 2). Stoat prints the error and full usage text to stderr. The
client-specific `mcp` flags are documented in [the MCP reference](mcp.md).

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

`--project` filters the list to VMs the `stoat.toml` in the current directory declares. It refuses outside a project.

**Exit codes:** 0 on success; 1 if the data root can't be read, or `--project` is given outside a project.

## `stoat init [--name n]`

Writes `stoat.toml` for this directory: one `[vms.dev]` declaration, annotated with every field's type and default. Refuses if `stoat.toml` already exists; there is no safe automatic merge into a file you already wrote.

```
$ stoat init
wrote stoat.toml
added .stoat/ to .gitignore
edit it, then run: stoat up
```

`--name` sets `project.name`, the prefix for a VM's global name; it defaults to the current directory's name, lowercased. In a git checkout, `init` also appends `.stoat/` to `.gitignore` if it is not already there.

**Exit codes:** 0 on success; 1 if `stoat.toml` already exists or the file can't be written.

## `stoat status`

Prints one line per VM `stoat.toml` declares: declaration key, global name, state, health, and every field where the declaration and the VM disagree.

```
$ stoat status
KEY          NAME                 STATE     HEALTH    DRIFT
dev          myrepo-dev           running   ok        cpus 2 → 4 (restart)
ci           myrepo-ci            missing   -         -
```

A VM `stoat.toml` declares but that does not exist yet shows state `missing`. An immutable-field mismatch (`image` or `disk`) prints in place of the drift column, naming `stoat rm <key>` as the fix.

**Exit codes:** 0 on success; 1 outside a project, or if a VM's status can't be read.

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

Flags: `--image` (required; catalog id or a path to your own image), `--os`, `--backend` (override what a bring-your-own image's filename would otherwise infer), `--mode` (`live` or `disk`; only meaningful for the alpine iso, every other image has one mode), `--ram` (MB), `--cpus`, `--disk` (absolute size, e.g. `8G`), `--share` (host directory to expose), `--console-password` (`random` generates one), `--recipes` (comma-separated or repeated), `--set recipe.param=value` (set a non-secret recipe parameter), `--secret recipe.param` (read a secret from the environment or prompt), `--agent-access` (`none`, `observe`, `manage`, or `exec`; default `manage`, controls MCP guest access). The hidden `--allow-exec` flag remains as a compatibility alias: true maps to `exec`, false to `manage`.

`create` (alias `new`) refuses at project scope: `a stoat.toml is present; declare the VM there and run stoat up, or pass --global`. `--global` creates the VM outside the project.

**Exit codes:** 0 on success; 1 if creation fails (e.g. the image isn't downloaded yet: run `stoat pull` or download it from the TUI's image picker first) or a `stoat.toml` refuses it without `--global`; 2 if `--image` is missing.

## `stoat update <name>`

Changes a VM configuration. **Only the supplied flags change fields.** An
omitted flag leaves its field unchanged, and an explicitly empty value clears
the field.

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

Flags: `--ram`, `--cpus`, `--ssh-port`, `--disk` (grow-only), `--share` (empty clears it), `--recipes` (empty clears it; replaces the whole list, it does not add to it), `--set recipe.param=value`, `--unset recipe.param` (remove a non-secret override and restore its manifest default), and `--secret recipe.param` (set a secret without writing its value to `vm.toml`). Use `recipe show` to inspect declared types, defaults, enum values, and required parameters.

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

At project scope, `<name>` is optional. A named VM is reconciled against its `stoat.toml` declaration before it starts, the same change `stoat update` would make. With no name, every declared VM is reconciled, then started, in declaration order; a VM that fails to reconcile or start stops the run, and every later VM is reported skipped.

On a Debian cloud VM, `shares` do not mount; see [the project file](project-file.md#shares).

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

At project scope, `<name>` is optional: with no name, every declared VM is stopped in declaration order, and a failure stops the run and reports every later VM as skipped.

The stop request can return while QEMU is still exiting. Use
`stoat wait <name> --until stopped` when a script needs confirmed termination.
Under project fan-out, add the VM name to keep the JSON output machine-readable;
no-name `down --json` can include progress prose before its result.

**Exit codes:** 0 on success; 1 if the VM can't be loaded, is broken, isn't running, or fails to stop.

## `stoat wait <name>`

Blocks until the VM reaches a state, or the timeout expires.

```
$ stoat wait work --until reachable
work reached reachable (1240ms)
```

`--until` is one of `reachable` (sshd answering on the VM's forwarded port, default), `applied` (the most recent recipe run finished), or `stopped` (qemu no longer running). `--healthy` waits for reachability and then every applied recipe's declared health check; it cannot be combined with an explicit `--until`. `--timeout` (default `2m`) is a Go duration (`30s`, `5m`).

A request that cannot ever be satisfied fails immediately rather than waiting out the timeout: `--until applied` on a VM with no recipes configured, or `--until reachable` on a VM that isn't running.

At project scope, `<name>` is optional: with no name, `wait` blocks on every declared VM in turn, in declaration order, and a VM that does not reach the state stops the run.

**Exit codes:** 0 if the state was reached; 1 if the timeout expires or the state can't be reached at all; 2 if `--timeout` is zero or negative.

## `stoat rm <name> [-y]`

Deletes a VM's directory. Refuses outright if it's currently running.

```
$ stoat rm scratch
delete VM scratch? [y/N] y
scratch deleted
```

Without `-y`, confirmation is required: interactively it prompts on stdout and reads a line from stdin (anything other than a `y`/`Y` aborts); in `-q`/`--quiet`/`--no-interactive` mode there is no prompt to answer, so it refuses outright instead of guessing. Under `--json` the same rule applies for the same reason: nothing reads stdin, so `-y` is required or the command fails with `confirmation_required`. `-y` skips the prompt in every mode.

At project scope, `<name>` is optional: with no name, every declared VM is asked for (or refused without `-y`) and deleted in declaration order.

**Exit codes:** 0 if deleted; 1 if the VM cannot be loaded, is running, the
confirmation is declined or aborted, `-y` was required but not supplied, or
the deletion fails. Declining the confirmation returns exit status 1.

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

Looks up `ssh` on `$PATH` and **replaces the current process** with it. Signals
and terminal behavior therefore match a direct `ssh` invocation.

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

`--broken` includes VMs whose `vm.toml` file cannot be parsed. `--images`
includes downloaded images that no VM references. Without either flag, `prune`
reports only partial downloads. The dry run and the applied run print the same
candidate list.

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

At project scope, `<name>` is optional: with no name, every declared VM's recipes run in turn, in declaration order, and a failure stops the run.

**Exit codes:** 0 on success; 1 if the VM cannot be loaded, is not running, or the apply fails. Cloud VMs support the same apply path over SSH; recipe run modes determine which scripts run or are skipped.

`provision` is a hidden alias of `apply`: `stoat provision work` behaves exactly like `stoat apply work`, and reports `"cmd":"apply"` under `--json`.

## `stoat recipes`

Lists recipes, optionally filtered to ones applicable to a guest OS and/or
backend. Applicability is determined by the guest OS, manifest `os`, and
manifest `requires`; the backend controls execution after selection. The
`--backend` flag remains accepted for compatibility with older callers but is
not an additional manifest-match condition.

```
$ stoat recipes --os alpine --backend apkovl
NAME                           DESCRIPTION
build-deps                     toolchain for building software from source
devtools                       git, a compiler, an editor and basic fetch tools
docker                         Docker engine and the compose plugin
pkg-tools                      tools for querying the package manager
python-dev                     Python 3 with pip and an isolated development environment
service-tools                  tools for inspecting services and the processes behind them
tailscale                      Tailscale daemon, installed and started (join manually)
xfce                           XFCE desktop with autologin startx on tty1
```

`--os` selects a guest OS. `--backend` is accepted but does not narrow the
manifest match. With neither flag, the command lists the full catalog.

**Exit codes:** 0 on success; 1 if the recipe list can't be built.

## `stoat check-recipes <names>... --os=OS`

Reports, for each named recipe, why it would **not** apply to the given OS/backend; an empty result means every one of them would.

```
$ stoat check-recipes xfce --os fedora --backend cloudinit
xfce: xfce is not offered to fedora/cloudinit
$ stoat check-recipes docker xfce --os debian --backend cloudinit
all applicable
```

`--os` is required. `--backend` is accepted for compatibility and is included
in the diagnostic wording, but it does not add a manifest applicability filter.

**Exit codes:** 0 on success, whether or not any recipe turned out inapplicable (an inapplicable recipe is a valid answer, not a failure); 1 if the check itself fails; 2 if no recipe names are given.

## `stoat recipe list`

Lists every visible recipe in shadow order and prints each recipe's scope and
remote commit when available. Project scope is active only when the current
directory contains `stoat.toml`. The JSON form also includes the roots in
search order.

```
$ stoat recipe list
NAME                 SCOPE     COMMIT   DESCRIPTION
build-deps           bundled            toolchain for building software from source
devtools             bundled            git, a compiler, an editor and basic fetch tools
docker               bundled            Docker engine and the compose plugin
pkg-tools            bundled            tools for querying the package manager
python-dev           bundled            Python 3 with pip and an isolated development environment
service-tools        bundled            tools for inspecting services and the processes behind them
tailscale            bundled            Tailscale daemon, installed and started (join manually)
xfce                 bundled            XFCE desktop with autologin startx on tty1
```

The bundled index currently has no remote entries. Use `recipe search` to
inspect the curated index before adding a remote recipe by name.

**Exit codes:** 0 on success; 1 if the directory can't be read.

## `stoat recipe show <name>`

Prints the recipe's schema, sorted named parameters and outputs, and its
declared health check without requiring a VM:

```
$ stoat recipe show docker
docker: Docker engine and the compose plugin
schema: 3
runtime: sh

params:
  user           string, default dev       account to add to the docker group

outputs:
  socket         path of the docker socket

health: docker info (timeout 30s)
```

Under `--json`, the result is `data.recipe` with the `RecipeSchema` documented
in [json.md](json.md). Secret parameter values are never part of this output;
the schema only says that a parameter has type `secret`.

## `stoat recipe new <name>`

Scaffolds a new recipe directory (manifest plus scripts) and prints its path.

```
$ stoat recipe new mytool --os alpine
/home/user/.stoat/recipes/mytool
edit its recipe.toml and scripts, then pick it in the new-vm form for a matching vm
```

`--backend` is accepted for CLI compatibility but does not change the scaffold:
all recipes are directories with a manifest and shell scripts. `-q` suppresses
the trailing hint line.

`recipe new` copies the annotated [recipe sample](samples/recipe.toml), with
`name` and `os` filled for the new recipe. It creates the default script and
every script path declared by the sample's `[scripts]` overrides. The strict
VM and guest samples are [here](samples/vm.toml) and
[here](samples/guest.toml).

**Exit codes:** 0 on success; 1 if the recipe can't be created (e.g. the name is already taken).

## `stoat recipe search [term...]`

Searches the curated remote index by recipe name and description. With no
terms it lists every index entry. `--refresh` refreshes the local index clone
before searching; the clone is otherwise reused for 24 hours.

```sh
stoat recipe search docker
stoat recipe search --refresh
```

The current shipped index may be empty. An index name can be passed to
`recipe add`; a Git URL is also accepted by `recipe add` but is not accepted by
the MCP `add_recipe` tool.

## `stoat recipe add <ref>`

Adds a remote recipe to the active scope. `<ref>` is an index name, an index
name with `@tag-or-branch`, or a Git URL with an optional `@ref`.

```sh
stoat recipe add my-tools
stoat recipe add my-tools@v1.2
stoat recipe add https://github.com/example/stoat-my-tools@main -y
```

An index name does not prompt. A Git URL previews the manifest and asks for
confirmation on a terminal; use `-y` for a non-interactive call. `--global`
selects the global lock and cache from a project. `--force` allows a remote
recipe to replace an existing bundled, local, or remote name.

In project scope, `recipe add` writes the declaration to `stoat.toml`, records
the resolved commit in `stoat.lock`, and populates `.stoat/recipes/` as one
transaction. Run `recipe lock` after you edit `[recipes]` manually, then run
`recipe sync` to update the cache from the lock.

Without a project file, global scope records the source in
`~/.stoat/stoat.lock` and checks it out under `~/.stoat/recipes/`.

## `stoat recipe lock [--global]`

Resolves each active project declaration to a full 40-character commit and
writes `stoat.lock`. It does not populate the recipe cache. In global scope,
the existing global lock is repinned. A stale or missing project declaration
must be locked before project recipe operations can proceed.

## `stoat recipe sync [--global]`

Makes the active cache match its lock. Missing or mismatched clean checkouts
are replaced, and project cache directories absent from the lock are removed.
A dirty checkout is refused; copy it to a local recipe before editing.

## `stoat recipe update [names...] [--global]`

Fetches the stored ref and repins it to a new commit. With no names, all
remote recipes in the active lock are updated. It does not search the index
again and refuses a dirty checkout.

## `stoat recipe rm <name> [-y]`

Removes a remote recipe's declaration, lock entry, and checkout. It refuses a
recipe still selected by a VM unless `--force` is supplied. Confirmation is
required unless `-y` is supplied; `--json` also requires `-y` because it never
reads stdin.

## `stoat guest ls`

Lists every loaded guest OS. User definitions in `~/.stoat/guests/*.toml`
override bundled definitions with the same name.

```
$ stoat guest ls
NAME       INIT     PKG      BACKEND    SOURCE
alpine     openrc   apk      apkovl     bundled
arch       systemd  pacman   cloudinit  bundled
debian     systemd  apt-get  cloudinit  bundled
fedora     systemd  dnf      cloudinit  bundled
ubuntu     systemd  apt-get  cloudinit  bundled
```

**Exit codes:** always 0.

## `stoat guest show <name>`

Prints one guest's merged definition: init system, shell, default backend and ssh user, escalate argv, capabilities, aliases, and the `pkg`/`svc` tables. See `docs/reference/guest.md` for what each field means.

```
$ stoat guest show alpine
name:             alpine (bundled)
init:             openrc
shell:            /bin/ash
default backend:  apkovl
default ssh user: root
escalate:         [sudo -n]
capabilities:     [apk openrc]
aliases:          []
pkg setup:        apk update
pkg install:      [apk --wait 60 add]
svc enable:       rc-update add {name} default
svc start:        rc-service {name} start
svc stop:         rc-service {name} stop
svc restart:      rc-service {name} restart
svc status:       rc-service {name} status
```

**Exit codes:** 0 on success; 1 if no guest by that name is loaded.

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

## `stoat screenshot <name> [-o path]`

Writes the VM's screen to a PNG and prints the path, pixel size, and byte count. QEMU sends its framebuffer through the QMP socket with `screendump`. This works with a GTK window or VNC display, including when the guest is at a boot prompt.

```
$ stoat screenshot work
/home/u/.stoat/work/screenshots/2026-09-05T140302Z.png (1280x800, 48213 bytes)
```

Without `-o`, the file is written to `<vm dir>/screenshots/`. Its name uses an
RFC3339 timestamp without colons. Additional screenshots in the same second
receive `-2`, `-3`, and later numeric suffixes.

`-o` names the file instead. qemu writes it as its own user, so a relative path resolves against the caller's working directory before qemu sees it.

**Exit codes:** 0 on success; 1 if the VM does not exist, is not running (`not_running`), or qemu refuses the dump (`screenshot_failed`).

## `stoat capabilities [VM]`

Reports the host checks, the implemented Stoat surfaces, the target access
limits, and the unavailable runtime proposals. It reads stored VM metadata
only. It does not start or contact a VM.

Omit the VM name for host and project scope. In a project, a bare name first
resolves against the current `stoat.toml`, then against a global VM name.
Without `--json`, Stoat prints a NAME, STATUS, SCOPE table. With `--json`, it
prints the standard result envelope carrying the schema 1 capability report.

MCP enforces `agent_access`. The CLI's own `stoat exec` and `stoat cp` do not.

## `stoat doctor`

Checks the same host prerequisites as the installer: QEMU/KVM, `qemu-img`,
`ssh`, `xorriso`, and `/dev/kvm` access.

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

**Exit codes:** without `--json`, 0 if every check passes and 1 if any check
fails. With `--json`, always 0 after a completed check; use the JSON `healthy`
field for host readiness.

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

A usage error (2) prints the complaint and full usage text to stderr. A runtime
failure (1) prints `stoat: <command>: <error>` to stderr. Without `--json`,
`exec` instead returns the guest command's status from 0 through 255. See
[`stoat exec`](#stoat-exec-name-command).

## Scripting

- **`-q`, `--quiet`, `--no-interactive`** are three names for the same flag, present on every subcommand. Where it has an effect, it suppresses the "in-progress" chatter (`starting work...`, `provisioning work...`, ...); final results and all errors print regardless of this flag.
- **`rm`** also treats `--no-interactive`/`-q`/`--json` as "there is no one to answer a confirmation prompt": without `-y` it refuses rather than blocking on stdin.
- **`--json`** is the machine-readable mode for named VM commands: one JSON object per line on stdout, errors included, implying `--quiet` and never prompting. Project fan-out `up`, `down`, and `apply` can currently add progress prose before the result; see [json.md](json.md) for the wire format and workaround.
- **`NO_COLOR`** (any non-empty value) disables ANSI color in `ls`'s output.
- Color is also **disabled automatically whenever stdout is not a terminal** (checked via `os.ModeCharDevice`), so piping `stoat ls` into `awk`, `grep`, or a file never carries escape codes even without setting `NO_COLOR`. Only `ls`'s `STATE` column is ever colored.
