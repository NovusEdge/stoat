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
- Verbs that are flows: repo add, reboot, wait-ready. These go to the recipe
  contract spec.
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
default_backend  = "cloudinit"          # iso.Entry.Backend overrides it
default_ssh_user = "freebsd"            # iso.Entry.SSHUser overrides it
escalate = ["sudo", "-n"]               # argv; applied only when ssh user != root
capabilities = ["rc", "pkg"]            # feeds recipe `requires` matching
aliases  = ["bsd"]                      # recipe [scripts] lookup: name, aliases, script
filename_hints = ["FreeBSD-"]
seed_packages  = ["sudo"]

[pkg]
setup   = "pkg update"
install = ["pkg", "install", "-y"]      # argv; apk carries "--wait 60"
scaffold_install = "pkg install -y "    # display text for `recipe new`
runtime_packages = { python3 = "python3" }

[svc]
enable  = "sysrc {name}_enable=YES"
start   = "service {name} start"
stop    = "service {name} stop"
restart = "service {name} restart"
status  = "service {name} status"

[cmd]
download = "fetch -o"                   # curl is absent from FreeBSD and Alpine base
useradd  = "pw useradd -n {name} -m"

[backend.cloudinit]                     # opaque to the loader; backend.For validates
skip_9p = true
```

Field rules:

- Required: every top-level scalar and list except `installer`, `aliases`,
  `filename_hints`; every `[pkg]`, `[svc]`, `[cmd]` key. The loader reports
  `guest.toml: <name>: missing <field>`.
- Unknown keys are an error, with the key name and line.
- `schema` is required and must equal 1.
- `[backend.*]` loads as `map[string]map[string]any`. `guest` stays a
  zero-import leaf; `backend.For` reads and validates its own table.
- `default_backend` and `default_ssh_user` are defaults. `iso.Entry` keeps
  precedence (`internal/backend/backend.go:56-62`).
- `escalate` is an OS fact (Alpine and OpenBSD ship `doas`). `sshx` applies it
  when `sshx.User(v) != "root"`.
- `{name}` in a `[svc]` or `[cmd]` template renders to the first argument.

Dropped from the first draft: `backend` and `ssh_user` (per-image facts),
`transport` (per-VM fact; Windows Server ships OpenSSH), `family`
(derivable from `capabilities`), `cloud_recipes` (no readers since recipe v2),
`[[images]]` (see Scope).

## Loading

`internal/guest` gains a loader:

1. Read every bundled file.
2. Read every `~/.stoat/guests/*.toml`.
3. A user file whose `name` matches a bundled one merges over it per field.
   A field set in the user file wins; an absent field keeps the bundled
   value. A user file with a new `name` must carry every required field.
4. Validate. Build `capabilityOSes` from every loaded guest's `capabilities`.

`Lookup(name) (OS, bool)` keeps its signature. Callers that degrade on
`false` today return an error instead:

- `internal/cloudinit/cloudinit.go:65` (falls back to `/bin/bash`)
- `internal/sshx/sshx.go:43` (falls back to `root`)

`stoat ls` shows a VM with an unknown `os` as `broken: unknown guest <os>`,
the same path a `vm.toml` parse error takes today.

### `internal/tomlx`

One decode helper for all three TOML file types:

```go
func Decode(path string, v any) error
```

- Wraps every error with the path.
- Rejects undecoded keys via `md.Undecoded()`, naming the key and line.
- Reads a `schema` field when the target type declares one; absence means 1
  for `vm.toml` and `recipe.toml`, since existing files have none.

`vm.toml` (`internal/config/config.go:205`) and `recipe.toml`
(`internal/recipes/manifest.go:44`) move to it in the same change. Neither
checks `Undecoded()` today, so a typo'd key is dropped silently.

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
| `cloudinit.go:133` `v.OS == "debian"` | `backend.cloudinit.skip_9p` |
| `scaffold.go:59` `PkgInstall` | `pkg.scaffold_install` |
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
stoat_download()    { fetch -o "$@"; }
stoat_useradd()     { pw useradd -n "$1" -m; }
STOAT_OS=freebsd; STOAT_INIT=rc; STOAT_PKGMGR=pkg
export STOAT_OS STOAT_INIT STOAT_PKGMGR
```

- Argv fields render as shell-quoted words. `{name}` renders as `"$1"`.
- The python3 runtime gets the three variables in the environment and a
  `stoat` module whose functions of the same names call `subprocess.run`.
- `cloudinit.WrapScripts` emits the identical prelude, so a recipe behaves
  the same over ssh and in `runcmd`.
- `[scripts]` override lookup order: exact `name`, then each `alias` in
  order, then `script`.
- `STOAT_PKGMGR` is the first entry in `capabilities` that names a package
  manager (`apk`, `apt`, `dnf`, `pacman`, `zypper`, `pkg`).

Bundled recipes: `docker`, `devtools`, `tailscale` keep their per-OS scripts.
`xfce` moves to one portable script over the verbs as the proof.

## CLI

New subcommand group:

- `stoat guest ls`: name, init, package manager, default backend, source
  (bundled or user).
- `stoat guest show <name>`: the merged definition. `--json` emits it as
  `wire.Guest`.

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
| `vm.toml.os` unknown | config | `unknown guest "freebsd"; run stoat guest ls` |
| `[backend.x]` bad key | backend.For | `guest freebsd: backend cloudinit: unknown key skip9p` |

## Testing

- Golden: the five bundled files reproduce today's `OS` values exactly.
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
`requires` values and the prelude. No Windows, NixOS, OpenBSD.
