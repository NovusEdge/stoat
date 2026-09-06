# The bundled recipe catalog

This page says what stoat bundles, why each entry is there, and what keeps the
set from growing without limit.

## Rules

- Two common developer recipes stay as they are: `devtools` and `python-dev`.
- A task that exists on every guest gets one recipe with a script per OS
  family. It does not get one recipe per OS.
- A recipe earns its place by needing real OS-specific behaviour. A package
  name that differs is not enough on its own; a package group, a pattern, or a
  different init system is.
- At most three OS-specific recipes per guest, for tasks with no counterpart
  elsewhere.
- Every recipe declares schema 3 outputs and a health check, applies safely a
  second time, and names no user account of its own.

## What is bundled

| Recipe | Guests | Purpose |
|---|---|---|
| `devtools` | all eight | Git, a compiler, an editor, basic fetch tools |
| `python-dev` | all eight | Python 3, pip, an isolated environment |
| `build-deps` | all eight | What building someone else's source tree needs |
| `service-tools` | all eight | Inspecting a running service and the processes behind it |
| `pkg-tools` | all eight | Querying the package manager beyond install and remove |
| `docker` | five | Docker engine and the compose plugin |
| `tailscale` | five | `tailscaled`, started and joined |
| `xfce` | four | XFCE with autologin on tty1 |

`devtools` and `build-deps` overlap on a compiler and `make`. The boundary is
the task: `devtools` equips the VM for writing code in it, and `build-deps`
equips it for compiling a project that expects autotools, `pkg-config`, and the
distribution's own packaging headers. On Arch the boundary does not hold:
`devtools` installs `base-devel`, and `build-deps` installs only `base-devel`,
so `build-deps` is a subset of `devtools` on that guest.

## The three new common recipes

Each installs the tooling for one task. None writes a report, because a report
generated at provision time is stale by the first time anyone reads it.

### build-deps

| Family | Packages |
|---|---|
| Debian, Ubuntu | `build-essential`, `pkg-config`, `autoconf`, `automake`, `libtool`, `dpkg-dev` |
| Fedora, AlmaLinux, Rocky | `@development-tools`, `pkgconf-pkg-config`, `rpm-build` |
| openSUSE | pattern `devel_basis` |
| Arch | `base-devel` |
| Alpine | `alpine-sdk`, `build-base` |

Outputs: `compiler`, `make`, `pkg_config`.
Health: each of the three responds to `--version`.

The RPM and openSUSE entries are a group and a pattern, which the package
manager expands. Arch's `base-devel` is a metapackage that pulls the same
tools in one name. That is the OS-specific behaviour this recipe exists for.

### service-tools

| Family | Packages |
|---|---|
| Debian, Ubuntu | `lsof`, `strace`, `procps` |
| Fedora, AlmaLinux, Rocky | `lsof`, `strace`, `procps-ng` |
| openSUSE | `lsof`, `strace`, `procps` |
| Arch | `lsof`, `strace`, `procps-ng` |
| Alpine | `lsof`, `strace`, `procps`, `openrc` |

Outputs: `service_manager` (`systemd` or `openrc`), `lsof`, `strace`.
Health: the service manager answers a status query, and `lsof -v` runs.

Alpine runs OpenRC and every other bundled guest runs systemd, so the health
check and the reported manager differ by guest rather than by package name.

### pkg-tools

| Family | Packages |
|---|---|
| Debian, Ubuntu | `apt-file`, `dpkg-dev` |
| Fedora, AlmaLinux, Rocky | `dnf-utils` |
| openSUSE | `zypper`, `libzypp` |
| Arch | `pacman-contrib` |
| Alpine | `apk-tools` |

Outputs: `query_tool` (the binary that answers "which package owns this file"),
`manager`.
Health: the query tool runs.

## OS-specific candidates, not yet selected

These have no counterpart on the other guests. None is implemented. Each needs
its own contract tests and its own live qualification before it is claimed.

| Guest | Candidate | Why it is OS-specific | Open question |
|---|---|---|---|
| AlmaLinux, Rocky, Fedora | SELinux tooling: `setroubleshoot-server`, `policycoreutils-python-utils` | SELinux exists only on the RPM family | Does a VM with SELinux in permissive mode need it? |
| openSUSE | `osc`, the Open Build Service client | OBS is openSUSE's own build service | Is a build-service client in scope for a local VM tool? |
| Arch | `namcap`, PKGBUILD linting | PKGBUILD is Arch's format | Overlaps `base-devel` from `build-deps` |
| Alpine | `abuild`, APKBUILD tooling | APKBUILD is Alpine's format | Overlaps `alpine-sdk` from `build-deps` |
| Debian, Ubuntu | `devscripts`, `lintian` | Debian packaging tooling | Overlaps `dpkg-dev` from `build-deps` |

Four of the five overlap a common recipe, which is the rule in this page doing
its job. Only the SELinux entry is clearly separate, and it needs a decision
about whether a permissive-mode VM benefits from it.

## Qualification

A recipe is claimed for a guest after it has been applied on that guest's
advertised release, from a binary built at a known commit, with the recipe
reporting healthy and its declared outputs resolving to real executables in the
guest. The retained evidence names the run, the binary hash, and the commit.
