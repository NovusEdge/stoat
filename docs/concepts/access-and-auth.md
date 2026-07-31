# Access and auth

Every VM stoat manages is reached over SSH, forwarded to a loopback port on
the host. There is no console-based login flow for day-to-day use, no
password, and no separate stoat-level credential system — it's all built on
one SSH keypair stoat generates for itself.

## The client keypair

The first time it's needed, stoat generates an ed25519 keypair with
`ssh-keygen` and stores it at `id_stoat` / `id_stoat.pub` in the data root
(`~/.stoat`, or `$STOAT_HOME`). This is implemented in `internal/keys/keys.go`.
The pair is created once and reused for every VM; if either half is missing,
stoat regenerates both rather than risk operating with a mismatched pair.

This client key's public half is what gets installed into every guest, so
stoat can reach it without a password.

## Getting the public key into a guest

How the key gets installed depends on the image type:

**Live Alpine VMs** (`internal/apkovl/apkovl.go`) — stoat builds an Alpine
`apkovl` overlay tarball that the initramfs unpacks at boot. It writes
`root/.ssh/authorized_keys` containing stoat's client public key, and enables
`sshd` as a default-runlevel service. Root's SSH access is what's configured;
there is no other user on these VMs.

**Cloud images** (`internal/cloudinit/cloudinit.go`) — a NoCloud cloud-init
seed ISO is built with a fixed `#cloud-config` `users:` block that creates a
`stoat` user (with passwordless sudo) and lists stoat's client public key
under that user's `ssh_authorized_keys`. The seed also sets `ssh_pwauth:
false`, so cloud-init never enables password authentication in the first
place.

Either way, the same single client keypair is the credential — stoat doesn't
mint a per-VM key.

## The guest host key (live VMs only)

Live Alpine VMs are rebuilt fresh on every boot, which would normally mean a
new SSH host key each time and a host-key warning on every connection.
`internal/keys/keys.go` avoids that by generating one more keypair — the
"guest host key" — once, and `apkovl.Build` bakes it into the overlay as
`etc/ssh/ssh_host_ed25519_key(.pub)` on every build. The host key is stable
across boots even though the VM itself is not persistent.

Cloud images don't need this: they aren't rebuilt from scratch, so cloud-init
generates their host keys normally on first boot.

## Connecting: which user, and how

`internal/config/config.go` defines `VM.SSHUser`, the account used for
SSH-based access and provisioning. It's read from `vm.toml` and left empty by
default; callers apply the default themselves rather than writing `"root"`
into every file. `iso.Catalog()` (`internal/iso/iso.go`) sets the right user
per image when a VM is created from the catalog:

| Image | Backend | SSH user |
|---|---|---|
| `alpine-standard`, `alpine-virt` | `apkovl` | `root` |
| `ubuntu-24.04`, `debian-13`, `fedora-cloud`, `arch-cloud` | `cloudinit` | `stoat` |

The `stoat` user is used for the cloud images specifically because that's the
user stoat's own cloud-init seed creates and keys — not each distro's usual
default user (e.g. `ubuntu` on Ubuntu's image).

`internal/sshx/sshx.go` builds the actual SSH invocation (`Args`), used for
unattended work like waiting on `sshd` and running recipe provisioning:

```
ssh -p <SSHPort> \
  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
  -o LogLevel=ERROR -o ConnectTimeout=5 -o BatchMode=yes \
  -i <data-root>/id_stoat \
  <SSHUser or root>@127.0.0.1
```

The connection always targets `127.0.0.1` on the VM's forwarded `SSHPort` —
QEMU forwards the guest's port 22 to that loopback port on the host, so
there's never a routable address involved.

The interactive path, used when you press `s` in the TUI (`internal/tui/ssh.go`),
builds a very similar argument list — same host-key options, same
`-i id_stoat`, same `root@127.0.0.1` target for live VMs — but deliberately
**omits `BatchMode=yes`**. `Args` (used for unattended provisioning) needs SSH
to fail immediately rather than block waiting for input that will never come.
The interactive path hands the terminal to a real, attended `ssh` process, so
if key auth doesn't work — for instance, a disk-mode VM that was installed
manually and never had the key provisioned — SSH is free to fall back to
prompting for a password at the terminal, the same as running `ssh` by hand.

## Why host key checking is off

Both paths set `StrictHostKeyChecking=no` and `UserKnownHostsFile=/dev/null`.
This is deliberate, not a shortcut: live Alpine VMs are rebuilt by stoat
constantly, and even with the stable guest host key described above, disk and
cloud VMs generate their own host keys independently. Strict checking would
mean a stale or mismatched `known_hosts` entry breaking a connection to a VM
stoat just built — the kind of false alarm host-key checking exists to avoid,
not the kind it's meant to catch.

## Passwords: console only, never over SSH

Over the network, authentication is by key and only by key. The cloud-init
seed sets `ssh_pwauth: false`, so a password is refused on the forwarded port
no matter what.

The QEMU **graphical console** is a different matter, and the three modes
behave differently:

| VM type | Console login |
|---|---|
| Alpine **live** | `root`, nothing to type — the account has no password |
| Alpine **disk** | whatever you set when you ran the guest's own installer |
| **Cloud** image | the `stoat` user, with the console password stoat set |

### Why cloud images need a password at all

cloud-init's `lock_passwd` defaults to **true**, so every account on a cloud
image starts locked, and these images ship with `root` locked besides. Before
stoat set a console password, that combination produced a login prompt in the
QEMU window with no valid answer at all: `root` locked by the image, `stoat`
with no password, and password auth refused over SSH.

New cloud VMs are created with a console password — the fixed, documented
value `stoat` by default, or a generated 32-character hex string if you pick
**random** on the `console` row of the new-VM form. Either way it appears on
the detail screen:

```
  ssh       stoat@127.0.0.1:2202
  console   stoat / stoat  (qemu window only)
```

and in the log line written when the VM starts, which you can read with
`stoat logs`.

### Why a fixed default is not a compromise

You end up at that console precisely when SSH *isn't* working, which is the
worst possible moment to go and look a credential up. And it is no weaker
than what is already there: anyone who can reach that console can read
`~/.stoat`, which holds the private key granting full access to the same VM.
The password is refused over the network, so it widens nothing.

If you would rather not have a value that is written down in this
documentation, choose **random** when creating the VM.

### VMs created before this existed

A cloud VM's seed is written once, when its overlay is first created, and
cloud-init reads it once at first boot. An existing VM therefore keeps
whatever it was created with. The detail screen says so plainly:

```
  console   no password set — console login is not possible
```

To fix one, either recreate it, or set `console_password` in its `vm.toml`
(`E` on the detail screen) and delete its `disk.qcow2` so the overlay and
seed are rebuilt on the next start — which discards everything in that VM.
