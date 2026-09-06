# Guest definitions

A guest OS's facts (its init system, package manager, service commands...)
live in one `guest.toml` file, not in Go. Eight come bundled
(`internal/guest/bundled/*.toml`: alpine, ubuntu, debian, fedora, arch,
almalinux, rocky, opensuse).
Add or override one by dropping a file in `~/.stoat/guests/<name>.toml`; see
`stoat guest ls` and `stoat guest show <name>`.

The catalog IDs for the three additional cloud images are `almalinux-9`,
`rocky-9`, and `opensuse-leap-16.0`. AlmaLinux and Rocky use a `12G` default
disk; openSUSE keeps the existing `8G` default. Catalog creation persists the
CPU pair `cpu_model = "host"` and `required_cpu = "x86-64-v2"`; legacy or
custom images leave both fields empty, and there is no CLI CPU override.
Older Stoat binaries ignore these optional fields and are not qualified readers
for the new guest definitions. For the three new guests, cloud-init omits the
automatic 9p mount; shared-directory support is not qualified for these
images. These catalog entries describe guest images and do not claim native
host OS or release support.

The FreeBSD definition below is a custom guest example, not catalog support.

```toml
schema  = 1
name    = "freebsd"
init    = "rc"                          # systemd | openrc | rc
shell   = "/bin/sh"
installer = ""                          # the interactive install command; "" means "the installer"
default_backend  = "cloudinit"          # seeds the VM field at create time; the VM field always wins after
default_ssh_user = "freebsd"            # same
escalate = ["sudo", "-n"]               # argv; applied only when the VM's ssh user is not root
capabilities = ["pkg"]                  # feeds recipe.toml's `requires`; the loader appends init
aliases  = ["bsd"]                      # extra keys a recipe's [scripts] map may use for this OS
filename_hints = ["FreeBSD-"]           # recognise this OS in a BYO image filename
seed_packages  = ["sudo"]               # packages the cloud-init seed assumes but the image may lack
log_path = "/var/log/messages"          # tail_log's fallback when a unit and a path are both omitted; optional

[pkg]
setup   = "pkg update"                  # prelude's stoat_pkg_setup; empty means no refresh needed
install = ["pkg", "install", "-y"]      # argv; prelude's stoat_pkg_install appends packages
scaffold_setup   = ""                   # comment text `recipe new` writes into a scaffolded script
scaffold_install = "pkg install -y "    # same, for the install line
runtime_packages = { python3 = "python3" }   # maps a recipe runtime to the package that provides it

[svc]
enable  = "sysrc {name}_enable=YES"
start   = "service {name} start"
stop    = "service {name} stop"
restart = "service {name} restart"
status  = "service {name} status"

[backend.cloudinit]                     # opaque to the loader; the cloudinit package decodes it
skip_9p = true
```

## Field rules

- Required: every top-level scalar and list except `installer`, `aliases`,
  `filename_hints`, `log_path`; every `[pkg]` and `[svc]` key except
  `scaffold_setup`. A missing one is `guest.toml: <name>: missing <field>`.
- Unknown keys are an error: `<path>: unknown key "<key>"`.
- `schema` must be present and equal to 1.
- The loader appends `init` to `capabilities`. A file whose `capabilities`
  names a different init system (`systemd`, `openrc`, `rc`) is rejected.
- `[backend.*]` loads as an opaque table per backend name. The backend
  package that owns a name decodes its own keys; `internal/guest` never
  reads inside.
- `default_backend` and `default_ssh_user` only seed a new VM's fields at
  create time. Every code path reads the VM's own field afterward, never
  these, so a catalog entry or a user's explicit choice always wins.
- `escalate` is an OS fact, not a VM fact: each guest definition supplies the
  command, and the bundled definitions use `sudo -n`. `seed_packages` records
  packages the cloud-init seed may need to provide, such as Alpine's `sudo`.
  `sshx` applies `escalate` only when the VM's ssh user is not root.
- Every `[svc]` value is a template: `{name}` renders to `"$1"`; a template
  without `{name}` gets `"$@"` appended instead. A template may not contain
  a single quote: the python prelude wraps each one in a single-quoted
  literal.
- `STOAT_PKGMGR` (available in the recipe prelude) is the basename of
  `pkg.install[0]`.

## Merging a user file over a bundled one

A user file whose `name` matches a bundled guest merges over it: scalars and
lists in the file replace the bundled value, an absent field keeps the
bundled one, and each `[backend.x]` table replaces whole (not merged key by
key). A user file with a new `name` needs every required field; nothing is
inherited.

`stoat guest show <name>` reports `source`: `bundled`, `user`, or
`bundled+user` for a merged file.

## Unknown guest

A `vm.toml` whose `os` names no loaded guest makes that VM broken (`stoat ls`
shows `unknown guest "<name>"; run stoat guest ls`), the same as an
unparseable `vm.toml`. An empty `os` is not checked: it keeps the existing
fallbacks for a VM saved before the OS field existed.
