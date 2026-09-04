# Guest definitions as data

Spec 1 of 4. Later specs depend on this format: recipe contract v3, remote
recipes, MCP exposure. Status: approved 2026-09-04.

## Goal

Add a guest OS to stoat by writing one TOML file. Today the OS list is a Go
literal (`internal/guest/guest.go:92-160`) and the per-OS runtime package
tables live in a second Go table (`internal/recipes/runtime.go:8-27`). The
recipe capability map (`internal/recipes/manifest.go:121-128`) is a third.
A new OS needs edits to all three, and a missing struct field fails silently.

## Scope

In: sh-shaped guests with an imperative package manager. Alpine, Debian,
Ubuntu, Fedora, Arch today; Rocky/Alma, openSUSE, FreeBSD are the first
targets for a new file.

Out, with a reason each:

- NixOS: no imperative package install or service enable; needs a
  declarative backend (config fragment plus `nixos-rebuild`).
- OpenBSD: no cloud-init in base; needs an `autoinstall(8)` backend.
- Windows: no `sh`; needs an unattend backend, PowerShell prelude, elevation
  as a token.
- Verbs that are flows or have no consumer here: third-party repo add,
  reboot, wait-ready, download, useradd. These go to the recipe contract
  spec. `pkg.setup` stays: it is the distro's own index refresh
  (`apk update`, `setup-apkrepos -c -1`), which every install needs first.
- Image catalog (`internal/iso`): stays in Go. Moving it inverts the
  `iso -> guest` import and drops catalog fields (`Flavor`, `Variant`,
  `Size`, `ChecksumURL`, `Notes`).

The guest file header states this limit so a later spec adds a second verb
model instead of bending this one.

## The file

Bundled files live in `internal/guest/bundled/<name>.toml` and embed via
`embed.FS`. User files live in `~/.stoat/guests/<name>.toml`.

```toml
schema  = 1
name    = "freebsd"
init    = "rc"                          # systemd | openrc | rc
shell   = "/bin/sh"
installer = ""                          # tui/provision.go reads it
default_backend  = "cloudinit"          # the VM field, set at create time, wins
default_ssh_user = "freebsd"            # same; the TUI form reads this default
escalate = ["sudo", "-n"]               # argv; applied only when ssh user != root
capabilities = ["pkg"]                  # feeds recipe `requires`; loader appends init
aliases  = ["bsd"]                      # recipe [scripts] lookup: name, aliases, script
filename_hints = ["FreeBSD-"]
seed_packages  = ["sudo"]

[pkg]
setup   = "pkg update"                  # prelude command; Provision runs it once
install = ["pkg", "install", "-y"]      # argv; apk carries "--wait 60"
scaffold_setup   = ""                   # comment text for `recipe new`; alpine's is 3 lines
scaffold_install = "pkg install -y "    # display text for `recipe new`
runtime_packages = { python3 = "python3" }   # arch maps python3 = "python" here

[svc]
enable  = "sysrc {name}_enable=YES"
start   = "service {name} start"
stop    = "service {name} stop"
restart = "service {name} restart"
status  = "service {name} status"

[backend.cloudinit]                     # opaque to the loader; the cloudinit package decodes it
skip_9p = true
```

Field rules:

- Required: every top-level scalar and list except `installer`, `aliases`,
  `filename_hints`; every `[pkg]` and `[svc]` key except `scaffold_setup`.
  The loader reports `guest.toml: <name>: missing <field>`.
- Unknown keys are an error, with the key name and line.
- `schema` is required and must equal 1.
- The loader appends `init` to `capabilities`. A file whose `capabilities`
  lists a different init name (`systemd`, `openrc`, `rc`) is rejected.
- `[backend.*]` loads as `OS.Backends map[string]map[string]any`. Each
  backend package decodes its own table from `Backends[name]` with `tomlx`
  rules (unknown key is an error). `backend.For` only dispatches.
- `guest` imports nothing from `internal/` except `tomlx`.
- `default_backend` and `default_ssh_user` seed the VM fields `Backend` and
  `SSHUser` at create time; the TUI form and `stoat new` read them. Code
  paths keep reading the VM fields (`internal/backend/backend.go:63-69`,
  `internal/sshx/sshx.go:39-44`).
- `escalate` is an OS fact (Alpine and OpenBSD ship `doas`). `sshx` applies it
  when `sshx.User(v) != "root"`, to recipe bodies, the runtime bootstrap, the
  share-mount script, and `cloud-init status --wait`.
- Every `[svc]` value is a template. `{name}` renders to `"$1"`. A template
  without `{name}` gets `"$@"` appended.
- `STOAT_PKGMGR` is the basename of `pkg.install[0]`.

Dropped from the first draft: `backend` and `ssh_user` (per-image facts),
`transport` (per-VM fact; Windows Server ships OpenSSH), `family`
(derivable from `capabilities`), `cloud_recipes` (no readers since recipe v2),
`[[images]]` (see Scope).

## Loading

`internal/guest` gains `Load(dir string) error`, called once at CLI and TUI
startup with `filepath.Join(config.Root(), "guests")`. Before `Load`,
`Lookup` sees bundled guests only, so a broken user file cannot take the
bundled set down.

1. Parse every bundled file at init; a bundled parse failure is a panic,
   since it is a build error.
2. Parse every `<dir>/*.toml`. The first bad file is `Load`'s error; the
   CLI prints it and exits 2, the TUI shows it in the status line and
   continues with bundled guests.
3. A user file whose `name` matches a bundled one merges over it: scalars
   and lists replace; `[pkg]` and `[svc]` merge per key; each `[backend.x]`
   table replaces whole. A user file with a new `name` must carry every
   required field.
4. Validate. Build the capability map from every loaded guest's
   `capabilities`.

`Lookup(name) (OS, bool)` keeps its signature. An empty `os` keeps today's
fallbacks (`/bin/bash`, backend from the VM field), since a BYO image
`iso.Infer` cannot name has one. A non-empty unknown `os` is an error raised
in `core.load` next to the TOML parse check (`internal/core/vm.go:54`), so
`stoat ls` shows `broken: unknown guest "freebsd"; run stoat guest ls`.
The three call sites that degrade on `false` today
(`internal/cloudinit/cloudinit.go:62`, `internal/backend/backend.go:66`,
`internal/cli/run_recipes.go:38`) keep their empty-`os` branch and lose the
unknown-name branch.

### `internal/tomlx`

One decode helper for all three TOML file types:

```go
func Decode(path string, v any) error
```

- Wraps every error with the path.
- Rejects undecoded keys via `md.Undecoded()`, naming the key and line.
- Reads a `schema` field when the target type declares one; absence means 1
  for `vm.toml` and `recipe.toml`, since existing files have none.
- Takes an option for unknown keys: `Reject` (error) or `Warn` (write
  `<path>:<line>: unknown key "x"` to a writer and continue).

`recipe.toml` (`internal/recipes/manifest.go:44`) moves to it with `Reject`:
recipes are hand-authored and a typo is the author's bug. `vm.toml`
(`internal/config/config.go:205`) moves to it with `Warn`: an older stoat
may have written a key a newer one dropped, and that must not mark the VM
broken. `guest.toml` uses `Reject`.

Library: BurntSushi/toml v1.6.0 stays. go-toml/v2 releases more often and has
`DisallowUnknownFields` built in; the helper isolates the choice to one file,
so a switch later is one change. koanf and viper rejected: a two-source merge
is a loop.

## Consumers rewired

| Today | After |
|---|---|
| `recipes/runtime.go` `installCommand`, `runtimePackage` | `pkg.install`, `pkg.runtime_packages` |
| `recipes/manifest.go:121` `capabilityOSes` | built from `capabilities` |
| `sshx.sudoWrap` hardcoded `sudo` | `escalate` |
| `cloudinit.go:83` seed packages | `seed_packages` |
| `cloudinit.go:133` `v.OS == "debian"` | `backend.cloudinit.skip_9p`, decoded in `cloudinit` |
| `scaffold.go:59-61` `PkgSetup`, `PkgInstall` | `pkg.scaffold_setup`, `pkg.scaffold_install` |
| `runtime.go` arch `python` special case | `arch.toml` `runtime_packages = { python3 = "python" }` |
| `guest.go` literal | five bundled TOML files; the literal is deleted |

## Verbs in recipes

`sshx.Provision` prepends a prelude rendered from the guest file to every
script body it pipes:

```sh
stoat_pkg_setup()   { pkg update; }
stoat_pkg_install() { pkg install -y "$@"; }
stoat_svc_enable()  { sysrc "$1_enable=YES"; }
stoat_svc_start()   { service "$1" start; }
stoat_svc_stop()    { service "$1" stop; }
stoat_svc_restart() { service "$1" restart; }
stoat_svc_status()  { service "$1" status; }
STOAT_OS=freebsd; STOAT_INIT=rc; STOAT_PKGMGR=pkg
export STOAT_OS STOAT_INIT STOAT_PKGMGR
```

- Argv fields render as shell-quoted words.
- `stoat_pkg_setup` is defined for recipes that need a second refresh after
  adding a repo. `Provision` runs `pkg.setup` itself once before the first
  recipe, and `BootstrapScript` runs it before installing an interpreter.
  `WrapScripts` puts it first in `runcmd`.
- If the body starts with a `#!` line, the prelude goes after that line.
- The python3 runtime gets a Python prelude that defines the same function
  names over `subprocess.run` and sets the three variables in `os.environ`.
  No module is shipped to the guest.
- `cloudinit.WrapScripts` emits the identical prelude, so a recipe behaves
  the same over ssh and in `runcmd`.
- `[scripts]` override lookup order: exact `name`, then each `alias` in
  order, then `script`.

Bundled recipes: `docker`, `devtools`, `tailscale` keep their per-OS scripts.
`xfce` moves to one portable script over the verbs as the proof.

## CLI

New subcommand group:

- `stoat guest ls`: name, init, package manager, default backend, source.
  Source is `bundled`, `user`, or `bundled+user` for a merged guest.
- `stoat guest show <name>`: the merged definition. `--json` emits
  `wire.Guest`: `name, init, shell, installer, default_backend,
  default_ssh_user, escalate[], capabilities[], aliases[], filename_hints[],
  seed_packages[], pkg{setup, install[], runtime_packages{}}, svc{},
  backend{}, source`. Lists are never null.

Both join `TestJSONEnvelopeEveryCommand`. The MCP layer adds `list_guests`
in spec 4 on top of `guest ls --json`.

Cleanup done in the same plan because `guest` would copy the pattern
otherwise:

- `provision` becomes a kong alias of `apply` instead of a duplicate struct
  and a duplicated `toArgs` case (`internal/cli/grammar.go:55,386-390`).
- A rule in `docs/design/core-api.md`: `a.fail` for an error from `core`,
  `a.failMsg` for a condition the CLI detects itself.

Deferred to the dev-standardization item: a typed envelope per command
instead of inline `map[string]any`, and a shared confirm helper for
destructive commands (`run_vm.go:241-257`).

## Migration

- Existing `vm.toml` files load unchanged. `os` values keep their names.
- Existing recipes load unchanged. `os = []` and `[scripts]` keys keep
  their names; `requires` gains the new capability names (`rc`, `pkg`,
  `zypper`).
- A user who edited `guest.go` before has no path; the Go literal is gone.
- `recipes/runtime.go` is deleted. `BootstrapScript` reads
  `pkg.runtime_packages` and `pkg.install`.

## Errors

| Condition | Where | Message |
|---|---|---|
| Missing required field | loader | `guest.toml: freebsd: missing pkg.install` |
| Unknown key | tomlx | `~/.stoat/guests/freebsd.toml:12: unknown key "pkg.instal"` |
| `schema` != 1 | tomlx | `...: schema 2 is newer than this stoat (1)` |
| `vm.toml.os` unknown | core.load | `unknown guest "freebsd"; run stoat guest ls` |
| `[backend.x]` bad key | the backend package | `guest freebsd: backend cloudinit: unknown key skip9p` |
| `vm.toml` unknown key | tomlx (Warn) | stderr: `~/.stoat/vms/x/vm.toml:7: unknown key "cpus_"` |
| bad user guest file | guest.Load | CLI exit 2; TUI status line, bundled guests still load |

## Testing

- Golden: for each of the five bundled files, the projection `{Name, Shell,
  Init, Installer, DefaultBackend, DefaultSSHUser, SeedPackages,
  FilenameHints, PkgInstall, RuntimePackages}` equals today's literal.
  Written before the literal is deleted, run against both.
- Loader: missing field, unknown key, wrong schema, per-field merge, new
  user guest with all fields, user guest missing a field.
- `tomlx`: unknown key with line number for each of the three file types.
- Prelude: rendered output per bundled guest is a golden file; a
  shell-syntax check (`sh -n`) runs on each.
- Capability map: built map equals today's `capabilityOSes` for the five.
- CLI: `guest ls`, `guest show`, `guest show nope` in the JSON envelope test.
- e2e (`scripts/e2e.sh`, opt-in, needs KVM): `xfce` applies on alpine
  through the verbs.

## Non-goals restated

No new backends. No image catalog change. No recipe format change beyond
`requires` values and the prelude. No Windows, NixOS, OpenBSD. No `[cmd]`
table; download and useradd verbs arrive with the recipe contract spec.
