# The Data Root

stoat keeps everything (VM configs, disks, keys, downloaded images, recipes,
logs) as plain files under one directory: the **data root**. There is no
database and no daemon; `vm.toml` is authoritative for a VM's state, and
stoat re-reads it from disk every time it needs to know anything.

## Where it lives

The data root is `$STOAT_HOME` if that environment variable is set,
otherwise `~/.stoat`. Every subcommand and the TUI itself calls an
`EnsureRoot` step on startup, which creates the root and its two fixed
subdirectories (`isos/`, `recipes/`) if they don't already exist, so a
completely fresh install ends up with a minimal, predictable layout rather
than an error.

## Layout

```
~/.stoat/
├── id_stoat                  # stoat's client SSH private key (ed25519)
├── id_stoat.pub               #   ...and its public half
├── guest_host_ed25519_key     # stable sshd host key baked into live VMs
├── guest_host_ed25519_key.pub
├── isos/                      # downloaded ISOs and cloud images
├── recipes/                   # global and bundled recipe scripts/fragments
├── shared/
│   └── <vm-name>/             # writable per-VM 9p work share, mounted at /mnt/work
├── stoat.lock                 # global recipe pins, when used outside a project
├── logs/
│   └── stoat.log              # one shared log for the whole tool
└── <vm-name>/                 # one directory per VM
    ├── vm.toml                # the VM's configuration, authoritative
    ├── disk.qcow2             # disk/cloud modes only
    ├── qemu.pid                # written by QEMU's -pidfile while running
    ├── monitor.sock            # QEMU monitor, unix socket, for Stop()
    ├── qmp.sock                # QMP socket, used for VM snapshots
    ├── vnc.sock                # VNC socket when the VM uses VNC display
    ├── console.log             # QEMU serial console log
    ├── last-provision.log      # output of the most recent `p` run, truncated each time
    └── ovl/
        ├── stoat.apkovl.tar.gz # live mode: rebuilt on every start
        ├── seed.iso             # cloud mode: cloud-init NoCloud seed (CIDATA)
        └── seed/
            ├── user-data
            └── meta-data
```

Project recipe state is stored beside the repository instead of in the data
root. `stoat.lock` contains project pins and `.stoat/recipes/` contains the
project recipe cache. `.stoat/secrets.toml` contains project recipe secrets and
must remain mode `0600`. `stoat init` adds `.stoat/` to `.gitignore` in a Git
checkout. A project cache and its secrets are separate from the global cache.

Two facts about that tree:

- `isos/` holds both plain ISOs (Alpine) and downloaded cloud images
  (Ubuntu/Debian/Fedora/Arch `.qcow2`/`.img` files). A `cloud` VM's
  `disk.qcow2` is a copy-on-write overlay backed by one of these, and the base
  image itself is never copied per-VM, only referenced.
- `ovl/` is reused for two unrelated purposes depending on mode: the Alpine
  overlay tarball for `live` VMs, or the cloud-init seed for `cloud` VMs. A
  `disk` VM has no `ovl/` contents until its first start builds the Alpine
  installer overlay; a non-Alpine BYO disk has no injected installer.
- `qemu.pid` and `monitor.sock` only exist while (or after) a VM has run at
  least once; they're not created at `vm.toml` save time.
- The SSH keypair and the guest host key are **not** per-VM: they live at
  the root of the data root and are shared by every VM stoat manages. See
  [Networking and sharing](networking-and-sharing.md) for how they're used.

## `vm.toml`

Each VM directory holds one `vm.toml`. Every field:

| Field | Type | Meaning |
|---|---|---|
| `name` | string | The VM's name; also its directory name under the data root |
| `mode` | string | `"live"`, `"disk"`, or `"cloud"`, see [Modes and backends](modes-and-backends.md) |
| `os` | string | The guest OS, e.g. `alpine`, `ubuntu`, `debian`, `fedora`, `arch` |
| `iso` | string | Path to the boot ISO, relative to the data root (e.g. `isos/alpine-...iso`) |
| `ram` | int | Memory in MB |
| `cpus` | int | Virtual CPU count |
| `disk` | string | Disk size, e.g. `"8G"` (`disk` mode only) |
| `installed` | bool | `disk` mode only. Flips the QEMU boot order: `false` keeps the installer ISO attached and boot-forced on every start; `true` boots straight off `disk.qcow2` |
| `share` | string | Legacy host directory exposed read-only to the guest as `/mnt/host`; empty means no share |
| `sshport` | int | The host-side loopback port forwarded to the guest's port 22 |
| `recipes` | []string | Recipe names selected for this VM and resolved through project, global, local, or bundled scopes |
| `backend` | string | `"apkovl"`, `"cloudinit"`, or `"ssh"` (recorded at creation time, informational only afterward) |
| `base` | string | Absolute path to the shared base image an overlay is created from (`cloud` mode only) |
| `sshuser` | string | The account used for SSH access/provisioning; empty means `root` (never written explicitly for that default) |
| `params` | table | Non-secret recipe parameters, grouped as `params.<recipe>.<name>` |
| `display` | string | Screen preference: empty/`auto`, `window`, or `vnc` |
| `forwards` | array | Declared host-to-guest TCP forwards |
| `console_password` | string | Graphical console password, primarily for cloud VMs; `random` is resolved when created |
| `allow_exec` | bool | Legacy per-VM permission for guest command and copy operations; new files use `agent_access` |
| `agent_access` | string | MCP access level: `none`, `observe`, `manage`, or `exec`; defaults to `manage` |
| `applied` | table | Recipe versions and health values written by stoat; do not edit |
| `project` | string | Absolute directory of the declaring `stoat.toml`; empty for a global VM |
| `shares` | array | Project directories exported under `/work`; stoat writes resolved paths and mount tags |

## What's safe to hand-edit

`vm.toml` is a plain TOML file and nothing stops you from editing it directly
while the VM is stopped: stoat re-reads it fresh every time, there's no
cache to invalidate. Some fields are safer to edit than others:

- **Safe-ish**: `ram`, `cpus`, `share`, `recipes` (as long as the names still
  resolve in the active recipe scope), `sshuser`.
- **Edit with care**: `sshport` (if you pick one another VM already has, both
  will try to bind it), `disk` (shrinking it doesn't shrink the underlying
  qcow2; you'd need a manual `qemu-img resize` and it can destroy data),
  `installed` (flipping it back to `false` on a VM whose disk already has an
  OS just means the ISO gets forced again on next boot, usually not what
  you want).
- **Don't hand-edit**: `iso`, `base`, and `mode`/`backend` together. These
  describe a specific boot/provisioning setup that other fields and files
  (the overlay, the cloud-init seed, the disk itself) were built to match;
  changing one without the others is how you end up with a VM that won't
  boot or a `vm.toml` that no longer describes reality. Use the TUI's edit
  form for these: it has guards against exactly this kind of mistake.

## Broken VMs

If a `vm.toml` exists but fails to parse (a stray edit with unbalanced
quotes, for instance), stoat does not silently drop that VM from the list.
A directory with a `vm.toml` that fails to parse shows up as a **broken**
row instead of vanishing, so you get a chance to notice and fix it rather
than wondering where a VM went. (A directory with no `vm.toml` at all,
by contrast, was never a VM as far as stoat is concerned, and is ignored
entirely.)

This matters for port allocation, too: a broken VM's `sshport` line is
still read out with a best-effort regex and reserved, even though the rest
of the file won't parse. Without that, creating a new VM could hand out the
same port a broken-but-still-real VM's disk image is already configured
for, a collision that's exactly how this safeguard came to exist.

## `isos/` and `recipes/`

`isos/` is never touched by VM deletion: removing a VM only removes its own
directory, never a shared ISO or cloud image other VMs might still be using.

`recipes/` starts out populated with stoat's bundled recipes the first time
it runs, but that install step never overwrites a file that's already there,
so local edits to a recipe survive a stoat upgrade. A project cache under
`.stoat/recipes/` takes precedence over a global remote recipe with the same
name; project, global, local, and bundled entries follow recipe scope
resolution.
