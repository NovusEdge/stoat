# CLI Reference

`stoat` with a subcommand runs non-interactively (`internal/cli`); with none, it launches the [TUI](tui.md) instead. No subcommand contains logic of its own — each is a thin wrapper over the same internal packages the TUI uses, so the two interfaces can't drift apart.

```
usage: stoat <command> [flags]
```

## Subcommands

| Command | Synopsis | Exit codes |
|---|---|---|
| [`ls`](#stoat-ls) | List VMs, one line per VM | 0, 1 |
| [`up`](#stoat-up-name) | Start a VM | 0, 1 |
| [`down`](#stoat-down-name) | Stop a VM (graceful) | 0, 1 |
| [`ssh`](#stoat-ssh-name) | ssh into a VM, replacing this process | 0, 1 |
| [`provision`](#stoat-provision-name) | Run recipes, streaming output to stdout | 0, 1 |
| [`rm`](#stoat-rm-name--y) | Delete a VM | 0, 1 |
| [`logs`](#stoat-logs--n-n) | Tail the stoat log | 0, 1 |
| [`doctor`](#stoat-doctor) | Check host prerequisites | 0, 1 |
| [`version`](#stoat-version) | Print the stoat version | 0 |
| [`help`](#stoat-help) | Show the usage message | 0 |

Every subcommand also accepts `-q` / `--quiet` / `--no-interactive` (three names for the same flag); anything not on this list, a missing VM name, or extra arguments is a **usage error** (exit 2), printed to stderr together with the full usage text.

## `stoat ls`

Lists every VM directory under the data root, plus any directory whose `vm.toml` failed to parse (shown with a `broken` state and a one-line reason) — broken VMs are real entries, not hidden.

```
$ stoat ls
NAME            MODE  STATE    CPUS RAM    SSH
work            live  running  4    4096   2222
scratch         disk  stopped  2    2048   2223
oldvm           -     broken   -    -      -    unexpected token near line 4
```

The `STATE` column is colored (green `running`, red `broken`) when [color is enabled](#scripting). `-q`/`--quiet` is accepted but has no effect on `ls`'s output.

**Exit codes:** 0 on success; 1 if the data root can't be read.

## `stoat up <name>`

Starts a VM.

```
$ stoat up work
starting work...
work started (ssh :2222)
```

`-q`/`--quiet`/`--no-interactive` suppresses the `starting <name>...` line; the final result line always prints.

**Exit codes:** 0 on success; 1 if the VM can't be loaded or fails to start.

## `stoat down <name>`

Stops a VM gracefully. Refuses if the VM isn't already running.

```
$ stoat down work
stopping work...
work stopped
```

`-q` suppresses the `stopping <name>...` line only.

**Exit codes:** 0 on success; 1 if the VM can't be loaded, isn't running, or fails to stop.

## `stoat ssh <name>`

Looks up `ssh` on `$PATH` and **replaces the current process** with it via `syscall.Exec` (the same as running `ssh` directly) — signals and the terminal behave exactly as a bare `ssh` invocation, and stoat leaves no supervisor process behind.

```
$ stoat ssh work
```

`-q` is accepted but has no effect (there is no chatter to suppress before the process is replaced).

**Exit codes:** 0 is not actually observed on success — the process image is gone. 1 if the VM can't be loaded, `ssh` isn't found on `$PATH`, or `exec` itself fails to launch.

## `stoat provision <name>`

Runs the VM's recipes over ssh, streaming `last-provision.log` to stdout as it's written (polled every 150ms) — the same log the TUI's detail screen tails, so there is no separate provisioning path to keep in sync.

```
$ stoat provision work
provisioning work...
=== recipe xfce ===
Unpacking libx11-data...
...
work provisioned
```

A **cloud-mode VM short-circuits**: cloud-init applies its recipes once, automatically, at first boot, baked into the seed when the VM's overlay was created — there is nothing left for ssh-based provisioning to do, and piping a cloud recipe (`#cloud-config` YAML, not a shell script) into `sh -s` would just fail. Instead it prints an explanatory line and exits 0 without touching ssh:

```
$ stoat provision cloudvm
cloudvm is a cloud VM — recipes are applied automatically via cloud-init at first boot; recreate the VM to change them.
```

`-q` suppresses the `provisioning <name>...` line only; the streamed log and the final line still print.

**Exit codes:** 0 on success (including the cloud short-circuit); 1 if the VM can't be loaded or the provision run itself fails.

## `stoat rm <name> [-y]`

Deletes a VM's directory. Refuses outright if it's currently running.

```
$ stoat rm scratch
delete VM scratch? [y/N] y
scratch deleted
```

Without `-y`, confirmation is required: interactively it prompts on stdout and reads a line from stdin (anything other than a `y`/`Y` aborts); in `-q`/`--quiet`/`--no-interactive` mode there is no prompt to answer, so it refuses outright instead of guessing. `-y` skips the prompt in either mode.

**Exit codes:** 0 if deleted; 1 if the VM can't be loaded, is running, the confirmation is declined or aborted, `-y` was needed but not given in quiet mode, or the delete itself fails. Note that declining the confirmation prompt is exit 1, not 0 — a script checking `$?` sees "delete didn't happen" as a failure either way, whether the VM was running or the user just said no.

## `stoat logs [-n N]`

Tails stoat's own log file (not a VM's provision log). `-n` sets how many lines from the end to print (default 50; `0` or negative prints the whole file).

```
$ stoat logs -n 20
...
```

**Exit codes:** 0 on success (including an empty log, which prints nothing); 1 if the log can't be opened or read.

## `stoat doctor`

Checks host prerequisites: that qemu/KVM preflight passes (`qemu.Preflight()`) and that `ssh` is on `$PATH`.

```
$ stoat doctor
ok
```

or, with problems:

```
$ stoat doctor
FAIL: /dev/kvm not accessible
FAIL: ssh not found in PATH
```

**Exit codes:** 0 if every check passes; 1 if any fails.

## `stoat version`

Prints the build's version string as `stoat <version>`. Equivalent to the top-level `-v` / `--version` flag (`stoat -v`, `stoat --version`), which is handled before any subcommand dispatch and produces identical output.

**Exit codes:** always 0.

## `stoat help`

Prints the full usage message (subcommands, global flags, exit codes) to stdout. The same text is printed to stderr — alongside the specific error — whenever a usage error occurs.

**Exit codes:** always 0.

## Exit codes

| Code | Meaning | Examples |
|---|---|---|
| `0` | Success | VM started/stopped, provisioned, deleted; `doctor` found nothing wrong |
| `1` | Runtime failure | Unknown VM name, VM already stopped for `down`, VM running for `rm`, ssh unreachable during provision, `doctor` found an issue, `rm` confirmation declined |
| `2` | Usage error | Unknown subcommand, missing/extra arguments, an unparseable flag |

A usage error (2) always prints both the specific complaint and the full usage text to stderr; a runtime failure (1) prints only `stoat: <command>: <error>` to stderr.

## Scripting

- **`-q`, `--quiet`, `--no-interactive`** are three names for the same flag, present on every subcommand. Where it has an effect (`up`, `down`, `provision`, `rm`), it suppresses the "in-progress" chatter (`starting work...`, `provisioning work...`, ...) — final results and all errors print regardless of this flag.
- **`rm`** additionally treats `--no-interactive`/`-q` as "there is no one to answer a confirmation prompt": without `-y` it refuses rather than blocking on stdin.
- **`NO_COLOR`** (any non-empty value) disables ANSI color in `ls`'s output.
- Color is also **disabled automatically whenever stdout is not a terminal** (checked via `os.ModeCharDevice`), so piping `stoat ls` into `awk`, `grep`, or a file never carries escape codes even without setting `NO_COLOR`. Only `ls`'s `STATE` column is ever colored.
