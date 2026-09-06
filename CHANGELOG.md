# Changelog

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
