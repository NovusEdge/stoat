# Recipes

A recipe is a small, named script that sets up something inside a guest
(XFCE, Docker, a dev toolchain, Tailscale) so you don't type the same install
commands into every VM you spin up. Recipes live as plain files on disk, in
`~/.stoat/recipes/` (or `$STOAT_HOME/recipes` if you've set that), and stoat's
picker offers you whichever ones make sense for the VM you're building.

## Two kinds

**Shell recipes** run as `root` over ssh, piped into `sh -s` on an
already-booted guest. They're the right shape for the apkovl (live Alpine) and
plain-ssh (installed disk) backends, where a real shell is sitting there
waiting for you.

**Cloud-config fragments** are `#cloud-config` YAML merged into the seed ISO
that cloud-init reads on a cloud image's first boot. They're the only option
for the cloudinit backend: a cloud image's `packages:`/`runcmd:` machinery runs
once, at first boot, before stoat ever gets an ssh session, there's no "run
this over ssh" step to hook into.

## Naming, and how the picker filters

The filename is the whole contract. `internal/recipes/recipes.go` reads it
straight off the directory listing: there's no manifest, no registration
step:

| Pattern | Kind | Offered to |
|---|---|---|
| `<name>.<os>.sh` | shell recipe | that exact OS, on the apkovl/ssh backends |
| `<name>.cloud.yaml` | shared cloud fragment | the OSes in the shared set (`ubuntu`, `debian`, `arch`), on the cloudinit backend |
| `<name>.<os>.cloud.yaml` | per-OS cloud fragment | that exact OS, on the cloudinit backend; takes the place of the shared fragment for that OS |

`List(osName, backend)` does the filtering: a shell recipe only ever shows up
for its exact OS, and cloud fragments only ever show up on the cloudinit
backend. `List("ubuntu", "cloudinit")` will never return `xfce.ubuntu.sh`, and
`List("alpine", "apkovl")` will never return `xfce.cloud.yaml`. If both a
per-OS fragment and the shared fragment could technically apply to one OS,
only the per-OS one is offered: you never see two "xfce" entries for the same
image.

`Install()` copies the bundled recipes into the data root the first time stoat
needs them, and (this matters if you ever edit one) **it never overwrites an
existing file**. Drop your own version in place of a bundled recipe, or add a
new one, and a stoat upgrade leaves it alone.

## When recipes run

- **Manually**, with `p` on the list or detail screen, against a VM's already-
  selected recipes (chosen when you created the VM, or later, see the edit
  form).
- **Offered automatically** after a start, once ssh comes up: stoat asks
  `<name> is up, run <recipe>, <recipe> now? y/N` rather than running anything
  unasked. A live VM gets asked every time (its root is wiped on every
  reboot, so nothing survives to make asking again redundant); a disk or cloud
  VM is only asked again if the last run didn't finish cleanly.
- **At first boot**, automatically, for cloud VMs: cloud-init applies the
  merged `packages:`/`runcmd:` fragment before you ever get an ssh prompt.
  There's nothing for `p` to do on a cloud VM; pressing it just tells you so.

A disk VM with no OS installed yet has no ssh to provision over at all:
`p` refuses with a reminder to run the installer at the QEMU console first.
See [troubleshooting](../troubleshooting.md) if you hit that.

## Bundled recipes

| Recipe | Files | What it does |
|---|---|---|
| **xfce** | `xfce.alpine.sh`, `xfce.arch.sh`, `xfce.ubuntu.sh`, `xfce.debian.sh`, `xfce.cloud.yaml`, `xfce.fedora.cloud.yaml` | Installs an XFCE desktop. The shell recipes autologin root on tty1 and start X; the cloud fragments install `lightdm` and give you a graphical login screen instead, since a cloud image has a real user account (`stoat`), so there is someone to log in *as*. |
| **docker** | `docker.alpine.sh` | Installs Docker plus the compose plugin (`docker-cli-compose`, a separate package from `docker` on Alpine) and starts the daemon. |
| **devtools** | `devtools.alpine.sh`, `devtools.cloud.yaml` | The baseline for a throwaway VM: `git`, `curl`, `ca-certificates`, `vim`, `tmux`, `less`. The Alpine version also installs `bash` (Alpine defaults to `ash`) and `build-base`, its gcc/make/libc-dev meta-package. |
| **tailscale** | `tailscale.alpine.sh` | Installs Tailscale and starts `tailscaled`. |

Two recipes have a cloud-config side, `xfce` and `devtools`. A shared
`<name>.cloud.yaml` covers Ubuntu, Debian and Arch, because cloud-init's
`packages:` list is handed straight to the guest's own package manager with no
per-distro syntax, so a shared fragment only works where the names happen to
match on both apt and pacman.

`devtools.cloud.yaml` deliberately drops the compiler toolchain that
`devtools.alpine.sh` installs: it is `build-essential` on apt, `base-devel` on
pacman and `@development-tools` on dnf: three names and three shapes for one
thing. Install your distro's own if you need it.

Fedora is the recurring exception and stays out of the shared set entirely:

- For xfce it has no package literally named `xfce4`, so it gets its own
  `xfce.fedora.cloud.yaml` using the comps group `@xfce-desktop`.
- For devtools, its `vim` is packaged as `vim-enhanced`, so even the shared
  fragment's names don't hold.

Worth knowing if you're writing your own: **Arch has no `xfce4` package
either**: `xfce4` is a package *group* there. It works only because
`pacman -S` accepts a group name where apt would want a real package. Do not
assume a name that resolves on two distros resolves the same way on both.

See [writing your own](writing-your-own.md) for the details if you're adding
another cross-distro recipe.

### Tailscale installs but does not authenticate

`tailscale.alpine.sh` installs the package and starts `tailscaled`, that's
where it stops. It does not run `tailscale up`, and it does not carry an auth
key. That's deliberate: stoat has nowhere to keep an auth key that isn't worse
than not having one. Put it in `vm.toml` and it sits in plaintext in the data
root; bake it into the recipe file and it ends up committed to git the first
time someone shares their recipes directory. Neither is acceptable, so the
recipe stops short and tells you the one command to run yourself:

```
tailscale installed and tailscaled running.
To join your tailnet, ssh in and run:  tailscale up
(stoat does not store auth keys, see this recipe's header for why.)
```
