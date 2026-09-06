# Changelog

## Unreleased

### Features

- Apply the `devtools` and `python-dev` recipes on AlmaLinux, Rocky, and
  openSUSE. All eight bundled guests now carry both recipes.
- Apply the `build-deps`, `service-tools`, and `pkg-tools` recipes on all eight
  bundled guests. `build-deps` installs a C compiler, `make`, and
  `pkg-config`, then reports each as an output. `service-tools` installs
  `lsof`, `strace`, and the process tools, then reports whether the guest runs
  systemd or OpenRC, and where `lsof` and `strace` are. `pkg-tools` installs the tool that
  answers which package owns a file, then reports that tool and the package
  manager.

## v0.3.0

Three enterprise Linux guests, common developer recipes, and read-only
capability discovery for agents. The JSON contract version stays **3**.

### Features

- Create VMs from AlmaLinux 9, Rocky 9, and openSUSE Leap 16.0 cloud images.
  Stoat checks the host CPU against the `x86-64-v2` baseline these guests
  require and reports a missing feature before it starts QEMU. These three
  guests carry no recipes and skip the 9p mount.
- Apply the `devtools` and `python-dev` recipes on Alpine, Arch, Debian,
  Fedora, and Ubuntu. `devtools` installs git, curl, a compiler, vim, tmux,
  less, and bash. `python-dev` installs Python and pip, then creates a virtual
  environment for a named user.
- Inspect agent-visible state with `stoat capabilities [VM] [--json]` and the
  MCP `capabilities` tool. Discovery reads configuration only. It creates no
  data root, generates no keys, and removes no stale PID files.
- MCP tools return machine-readable errors with stable codes.

### Fixes

- Cloud VMs wait for cloud-init to finish before provisioning starts. A
  recoverable cloud-init warning no longer blocks the wait; a hard error still
  fails it.
- Native VM operations that the host cannot support now fail with a named
  reason instead of an unqualified QEMU error.
- File locks work on every supported host.
- A seed that holds one cloud-config document is sent as plain
  `#cloud-config`. cloud-init 24.4 on AlmaLinux and Rocky fails its init-local
  stage on the archive's top-level list, which left `cloud-init status` in
  error for the life of the VM.
- The `python-dev` recipe tests each privilege-escalation tool before it uses
  one. Alpine's sudoers grants root nothing, and busybox `su` takes options
  from the trailing arguments. Both broke the recipe on Alpine.
- The guides state the runtime and interface behavior that stoat implements.

### Known limitations

- A cloud VM with recipes on AlmaLinux or Rocky still fails, because the seed
  keeps more than one document. See #93.
- The `devtools` and `python-dev` recipes do not cover AlmaLinux, Rocky, or
  openSUSE. See #85.
- `docs/specs` describes VM forks and runtime continuation. Neither is
  implemented.

## v0.2.0

First published release, for single-user Linux systems. This is a regular
release rather than a prerelease; its version number is below 1.0.

### Features

- Declare project VMs in `stoat.toml`. `stoat init`, `status`, and `ls --project`
  support project setup and inspection; `up`, `down`, `apply`, `wait`, and `rm`
  can operate on every declared VM when called without a name.
- Serve MCP directly with `stoat mcp`. The Go server replaces the Python
  wrapper and adds guest file operations, package and service management,
  background jobs, project tools, and client configuration helpers.
- Control agent access per VM with `none`, `observe`, `manage`, and `exec`.
  New VMs default to `manage`; an agent cannot raise its own access through
  the MCP update tool.
- Recipe manifests support typed parameters, secrets, outputs, and health
  checks. Inspect them with `stoat recipe show` and wait for recipe health
  with `stoat wait --healthy`.
- Install remote recipes from Git, pin commits in `stoat.lock`, and share
  declarations at project or global scope. The curated index lives in this
  repository's `index.toml`.
- Define guest OS behavior in TOML, with bundled Alpine, Arch, Debian, Fedora,
  and Ubuntu definitions and user overrides under the Stoat data root.
- Capture VM screens with `stoat screenshot`. JSON callers receive structured
  results and stable error codes for QEMU, image, and configuration failures.

### Fixes

- Installed Alpine VMs recover from the hidden boot-menu stall, mount their
  work share correctly, and show the normal login banner. The XFCE recipe
  installs the X server again.
- Cloud recipe waits resolve from applied state. `up --json` waits for its
  automatic apply and wraps recipe output as JSON events.
- Missing required recipe secrets return `invalid_spec`. Legacy `allow_exec`
  values stay consistent with the effective agent access level.
- Project drift reports use catalog image names and normalize disk sizes.
- The Nix package uses the current dependencies and reports `v0.2.0`.

### Upgrading from development builds

- JSON contract version is **3**, independently of the CLI version.
  `recipe list --json` returns `roots` and recipe entry objects; consumers
  must read `data.recipes[].name` instead of treating entries as strings.
- Replace Python MCP launch configurations with `stoat mcp`.
- Existing `allow_exec = true` maps to `exec`; `false` maps to `manage`.
- Existing local edits to bundled recipes are preserved during installation.
  Review those copies separately when adopting the bundled recipe fixes.

### Known limitations

- VM execution requires an x86-64 Linux host with KVM. The arm64 CLI archive
  is cross-compiled; the current `qemu-system-x86_64` backend does not support
  starting VMs on an arm64 host.
- Debian cloud kernels lack the 9p module, so project shares are skipped on
  Debian cloud VMs. Ubuntu cloud VMs support the project shares.
- The intended `up --json` wait during disk installation still needs a
  dedicated live test. Earlier live gates covered Alpine disk boot fixes,
  Debian cloud MCP execution, and Debian/Ubuntu project workflows.
