# The project file

`stoat.toml` in a repository declares that repository's VMs. `git clone`
the repository, then run `stoat up`, to build them.

## Scope

`stoat.toml` in the current directory activates project scope. There is
no walk-up: a parent directory's `stoat.toml` has no effect.

## The file

```toml
# stoat.toml declares this repository's VMs. Commit it, and stoat.lock with it.
# Every field carries its type, its default, and who writes it.
schema = 1                      # int, required. The file format version.

[project]
name = "myrepo"                 # string, default: the directory name.
                                 # The prefix for a VM's global name.

# Remote recipes. A value is a ref string, or a table naming a source.
# stoat recipe lock pins each one to a commit in stoat.lock.
[recipes]
tailscale = "v1.2"

[vms.dev]                       # the key "dev" is the name you type
image        = "ubuntu-24.04"   # string, required. A catalog id, or a path
                                 # to your own image, relative to this file.
name         = "shared-dev"     # string, default "<project>-<key>". The VM's
                                 # global name under ~/.stoat.
cpus         = 4                # int, default 4
ram          = 4096             # int, MB, default 4096
disk         = "20G"            # string, default 8G. Disk-mode images only.
recipes      = ["docker", "tailscale"]  # applied in dependency order
shares       = [".", "src"]     # directories from this project, mounted under
                                 # /work. "." mounts at /work, "src" at
                                 # /work/src. Every entry stays inside the
                                 # project.
agent_access = "manage"         # none | observe | manage | exec, default manage

[vms.dev.params.docker]         # non-secret recipe params
user = "dev"                    # secrets go in .stoat/secrets.toml, 0600

[vms.docs]
image  = "alpine-virt"
shares = ["docs"]
```

See the [sample file](samples/stoat.toml) on its own.

## Names

A VM's global name is its declaration's `name` field, if set, otherwise
`<project>-<key>`. `project.name` defaults to the repository directory
name.

A bare command argument resolves to the declaration key first, then to a
global name. `stoat ssh dev` reaches `shared-dev`.

Two declarations that resolve to one global name are an error.

## Shares

Each `shares` entry mounts read-write under `/work` in the guest. `.`
mounts at `/work`. Every other entry mounts at `/work/<basename>`.

Every entry must resolve inside the project directory. A relative path
that escapes it, directly or through a symlink, is refused.

Shares do not mount on a Debian cloud VM. Debian's cloud kernel has no 9p
module, so the mount would fail on every boot. Debian's `guest.toml` sets
the `skip_9p` flag under `[backend.cloudinit]`, and stoat skips the mount
step there. Ubuntu cloud VMs mount shares as usual.

## Reconcile

`stoat up` reconciles a declared VM before it starts it:

- A missing VM is created from its declaration.
- An existing VM takes `cpus`, `ram`, `recipes`, `params`, `shares` and
  `agent_access` from the declaration, through the same path as `stoat
  update`. `cpus`, `ram` and `shares` take effect at the VM's next `down`
  and `up`.
- `image` and `disk` are immutable. A declaration that changes either is
  an error naming `stoat rm <key>` as the fix.

## Secrets

Secrets live in `.stoat/secrets.toml`, mode 0600, keyed
`<key>.<recipe>.<param>`. `stoat init` adds `.stoat/` to `.gitignore` in
a git checkout. Every reader renders a secret as `<set>` or `<unset>`,
never as its value.

## Commands

| Command | Effect |
|---|---|
| `stoat init [--name n]` | writes `stoat.toml` from the annotated sample, with one VM |
| `stoat status` | one line per declared VM: global name, state, health, drift |
| `stoat ls --project` | filters the VM list to the current project |
| `stoat up`, `down`, `apply`, `wait`, `rm` with no VM argument | act on every declared VM, in declaration order |

## Errors

| Condition | Message |
|---|---|
| duplicate global name | `stoat.toml: vms.dev and vms.ci both resolve to "myrepo-dev"` |
| share outside project | `stoat.toml: vms.dev.shares: "../secrets" is outside the project` |
| immutable change | `dev: image changed (ubuntu-24 → debian-12); run stoat rm dev and stoat up` |
| new at project scope | `a stoat.toml is present; declare the VM there and run stoat up, or pass --global` |
| unknown key in a bare argument | `no VM "db" in stoat.toml or ~/.stoat/vms` |
