# Changelog

## Unreleased

### Added

- The curated recipe index has its first entry, `caddy`, which installs the
  Caddy web server and serves one directory over HTTP on Alpine, Ubuntu,
  Debian, Fedora and Arch. `stoat recipe add caddy` now resolves a name
  instead of needing a repository URL. `docs/recipes/sharing.md` describes how
  to publish an entry.

### Fixes

- `stoat update <vm> --agent-access exec` set the level without updating the
  legacy `allow_exec` field beside it, so the VM reported `allow_exec: false`
  in every JSON and MCP payload while exec worked. The two stay in step now.

## v0.6.1

Fixes found by driving the MCP tools against a live VM. The JSON contract
version stays **3**, and nothing changes about how you use stoat.

### Fixes

- `job_status` reported `unknown` for a job that had just started, which the
  tools define as a job whose guest side is gone. An agent polling right after
  `exec_bg` read a healthy job as lost. A job that has not recorded its pid yet
  now reports `starting`.
- An Alpine VM's console log stayed empty, so `stoat logs` had no record of a
  boot that failed. The kernel now writes to the serial console as well as the
  qemu window.
- A relative guest path, a host path outside a VM's shared directory, and a
  project tool with no `stoat.toml` all answered with the error code
  `internal`, which reads as a fault in stoat. They answer `usage` now. The
  project message also names the directory the server is serving, since a
  client started elsewhere has no other way to see it.
- `stoat mcp`'s `wait` tool described `healthy` as an extra condition on
  `until`, while both the tool and the CLI treat it as its own state.
- A `restore` on a live VM said "no disk to snapshot", and a failed VM start
  printed "qemu failed to start" twice.
- `stoat down` waits on the process itself on Linux instead of polling, so it
  returns as soon as QEMU exits.

## v0.6.0

Stoat can hold limits on what an agent starts, keep an MCP server running in
the background, and it now confirms that a VM actually stopped. The JSON
contract version stays **3**.

```sh
stoat mcp up                 # background MCP server for this project
stoat mcp status
stoat mcp down
```

### Limits

`[limits]` in `~/.stoat/config.toml` caps what exists and what runs:

```toml
[limits]
max_vms = 8
max_ram_mb = 16384
```

`max_vms` counts every VM in the data root and `create` refuses past it.
`max_ram_mb` sums the RAM of running local VMs and `up` refuses a start that
crosses it. An unset key means no limit, so nothing changes until you set one.

A project's `stoat.toml` takes the same table and may only lower it. That file
lives in your repository, where an agent that writes files could otherwise
lift its own ceiling.

Whether or not you set a limit, `up` refuses a VM that asks for more memory
than the host has free. `stoat up -y` starts it anyway. The MCP server has no
such flag, so an agent gets the refusal.

### Changes

- `stoat mcp up`, `down` and `status` run one HTTP MCP server per project in
  the background. `up` waits for the address to answer, so a bound port fails
  at the prompt. Nothing restarts a server after a reboot.
- `stoat down` waits for QEMU to exit, and kills what ignores SIGTERM. It used
  to send the signal and report success while the process kept running, which
  is how a host collected VMs nothing admitted were up.
- A project-wide `up` that fails part way now says how many VMs are still
  running, and that `stoat down` stops them.
- `stoat ls`, `status` and the TUI's first paint fetch VM status in parallel.
  A fleet of GCE instances paid one network round trip per VM, in series.

## v0.5.0

Stoat can run a VM on Google Compute Engine instead of on your own machine.
Local VMs work exactly as before. The JSON contract version stays **3**.

```sh
gcloud auth application-default login
stoat create dev --provider gce --image ubuntu-24.04 --ram 4096 --cpus 2
stoat up dev
stoat exec dev -- uname -a
```

Put your project and zone in `~/.stoat/config.toml`. Without them stoat reads
gcloud's own settings and prints which it used, so a VM never lands quietly in
the wrong project.

See [the GCP guide](docs/guides/gcp.md) for the whole path.

### Changes

- `--provider gce` creates a Compute Engine instance. `exec`, `cp`, recipes,
  services and the file tools all work on it.
- Ubuntu is the only guest for now. Debian's images on GCE have no cloud-init,
  so stoat cannot set them up, and it says so rather than leaving you with a
  half-built VM.
- VMs stop themselves after a day, so one you forget stops costing money.
  Change it with `max_run_duration`, or push a VM's deadline back with
  `stoat gce extend dev 4h`.
- `stoat prune` finds VMs on GCP that stoat lost track of, and ones you stopped
  whose disk still costs money. It reports them; it never deletes them.
- SSH is opened to your address only, and the VM's host key is checked. Set
  `source_range` if stoat guesses your address wrong.
- Things a cloud VM cannot do, like snapshots and screenshots, say so.
- `stoat ls` shows where each VM runs. `stoat get` shows the project, zone,
  address and when the VM stops. Both appear in the TUI.

### Under the hood

`internal/provider` is the seam a second execution surface plugs into. Non-QEMU
VM records live under `$STOAT_HOME/v2/`, where an older stoat cannot see them
and delete one while its instance keeps billing.

## v0.5.0-alpha.2

A pre-release. Stoat can now run a VM on Google Compute Engine instead of on
your own machine. Local VMs are unchanged. The JSON contract version stays **3**.

```sh
gcloud auth application-default login
stoat create dev --provider gce --image ubuntu-24.04 --ram 4096 --cpus 2
stoat up dev
stoat exec dev -- uname -a
```

Project and zone go in `~/.stoat/config.toml`. Without them stoat reads
gcloud's own settings, and prints which it used, so a VM never lands quietly in
the wrong project.

### Changes

- `--provider gce` creates a Compute Engine instance. `exec`, `cp`, recipes,
  services and the file tools all work on it.
- Ubuntu is the only guest for now. Debian's images on GCE have no cloud-init,
  so stoat cannot set them up, and it says so instead of leaving you with a
  half-built VM.
- Instances stop themselves after a day, so one you forget stops costing money.
  Change it with `max_run_duration`.
- SSH is opened to your address only, and the VM's host key is checked. Set
  `source_range` if stoat guesses your address wrong.
- Things a cloud VM cannot do, like snapshots and screenshots, say so instead
  of failing oddly.
- `stoat ls` shows where each VM runs. `stoat get` shows the project, zone,
  address and when the VM stops. Both appear in the TUI too.

### Not yet

`stoat prune` does not look for VMs on GCP, and there is no way to extend a
VM's deadline yet. Both come next.

## v0.5.0-alpha.1

A pre-release with no new features. It is groundwork for running VMs somewhere
other than your own machine.

Nothing changes for you in this release. Your VMs work the same way, your
`vm.toml` files load unchanged, and QEMU on Linux with KVM is still the only
way to run a VM. The JSON contract version stays **3**.

### Changes

- Stoat now asks a "provider" where a VM runs, instead of talking to QEMU
  everywhere. QEMU is the only provider today. `vm.toml` accepts a `provider`
  key, and leaving it out means QEMU.
- SSH connections carry their address explicitly. A VM reached over the
  internet will check its host key; a local VM keeps the settings it had.
- `stoat clone` checks whether the VM it copies is running the same way every
  other command does.

## v0.4.2

Release archives for macOS and Windows. The JSON contract version stays **3**.

### Changes

- Release archives cover darwin (amd64, arm64) and windows (amd64, arm64)
  next to the linux tarballs. Windows ships a zip. Those binaries run `doctor`
  and `capabilities` only until #82 and #83 qualify the hosts.

## v0.4.1

Plainer CLI output and a shorter `stoat.toml` sample, plus the fixes that
landed after v0.4.0. The JSON contract version stays **3**.

### Changes

- `stoat init` reports `stoat.toml created in <dir>` and stops. It no longer
  tells you to edit the file and run `stoat up`.
- The `stoat.toml` sample `init` writes carries short comments. The full
  field-by-field reference stays in `docs/reference/samples/stoat.toml`.
- CLI messages use short sentences in place of semicolon clauses. This covers
  the display lines, `prune --dry-run`, `clone`, `update`, `forward`, and a
  failed disk install.
- `stoat recipe new` prints the manifest path alone.

### Fixes

- `python-dev` derives `venv_dir` from the SSH account. (#116)
- Recipe params with `default_from` prefill from the VM's SSH account in the
  TUI, and `default_from` appears in the JSON contract. (#107, #108, #110)
- `stoat recipe refresh` reports a manifest entry that disappeared. (#109)
- A Windows drive letter in a path argument reads as a local path, not a VM
  name. (#113)
- The unsupported-host refusal names what to do next. (#112)
- Alpine repository setup moved into `stoat_pkg_setup`. (#111)

## v0.4.0

A bundled recipe catalog with a rule, three new common recipes on every guest,
and a cloud path that needs no external tool. The JSON contract version stays
**3**.

### Features

- Apply the `devtools` and `python-dev` recipes on AlmaLinux, Rocky, and
  openSUSE. All eight bundled guests now carry both recipes.
- Apply the `build-deps`, `service-tools`, and `pkg-tools` recipes on all eight
  bundled guests. `build-deps` installs a C compiler, `make`, and
  `pkg-config`, then reports each as an output. `service-tools` installs
  `lsof`, `strace`, and the process tools, then reports whether the guest runs
  systemd or OpenRC, and where `lsof` and `strace` are. `pkg-tools` installs
  the tool that answers which package owns a file, then reports that tool and
  the package manager.
- Create a cloud VM without `xorriso` on the host. Stoat writes the cloud-init
  seed image itself. `xorriso` is now needed only to install an Alpine disk VM,
  and `stoat doctor` reports it as optional.

### Fixes

- `stoat up` no longer hangs on a guest whose cloud-init keeps its run
  directory root-only. Stoat polls `cloud-init status` on its own deadline and
  retries an unreadable probe under the guest's escalation. Fedora 44 ships the
  cloud-init version that caused the hang.
- `pacman` skips a package the Arch guest already has. A VM that selects both
  `devtools` and `build-deps` reinstalled the whole `base-devel` group.
- The terminal UI follows the terminal's own background colour.

### Known limitations

- `docs/specs` describes VM forks and runtime continuation. Neither is
  implemented.
- Native VM operations run on Linux only. macOS and Windows report an
  unqualified host. See #82 and #83.

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
