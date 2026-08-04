# Writing your own recipe

Authoring a recipe needs no code change. Drop a correctly-named file in
`~/.stoat/recipes/` and it appears in the picker the next time you open it:
`recipes.List` just reads the directory. Because `Install()` never overwrites
an existing file, your recipe (and any edit you make to a bundled one) survives
a stoat upgrade.

See [the overview](overview.md#naming-and-how-the-picker-filters) for the full
naming table. The short version for a shell recipe: name it
`<name>.<os>.sh`, e.g. `~/.stoat/recipes/postgres.alpine.sh`, and it's offered
to Alpine VMs on the apkovl/ssh backends.

## The required shape

Every bundled shell recipe starts the same way, and yours should too:

```sh
#!/bin/sh
set -e
```

`set -e` matters more here than in a script you'd run locally: a recipe runs
unattended, streamed into `sh -s` over ssh
(`internal/sshx/sshx.go`'s `Provision`), with its output going straight to
`last-provision.log`. Without `set -e`, a failed package install just scrolls
past and the recipe reports success anyway.

The recipe runs as `root`: there's no `sudo` to reach for, and none is
installed on Alpine by default.

On Alpine, enable the community repository before installing anything from it,
most non-base packages (Docker, Tailscale, `build-base`) live there, not in
main:

```sh
setup-apkrepos -c -1
```

`-c` turns on community; `-1` picks the fastest mirror and refreshes the
package indexes in the same step, so a separate `apk update` afterward would
just be redundant work that widens the window for a transient network drop to
kill the whole recipe under `set -e`.

## The live-vs-disk honesty block

This is the one thing every bundled shell recipe carries, and
`recipes_test.go` enforces it (`TestShellRecipesAreHonestAboutLiveVsDiskPersistence`)
for every file in the bundle. It exists because of a real trap: a **live**
Alpine VM boots into a diskless mode where the root filesystem is a
`tmpfs`/`overlay` mount in RAM. Anything your recipe installs or edits there,
packages, `/etc` files, `/root/.profile`, is gone the moment the VM reboots.
A **disk** VM, by contrast, has a real block-device root that persists
normally. The same recipe file runs against both, over the same kind of ssh
session, so it has to check which one it landed on rather than assume:

```sh
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
    echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot; rebooting will NOT bring it back. Use a disk VM to keep it."
    ;;
*)
    echo "installed on a disk VM (root is $root_fstype): this survives a reboot."
    ;;
esac
```

Skip this and your recipe can end up promising something false: "reboot to
get your desktop" is a lie on a VM whose root is tmpfs. If your recipe does
something that needs to appear *right now* on a live VM rather than after a
reboot that will never come (the way `xfce.alpine.sh` sends `kill -HUP 1` to
make init respawn tty1 immediately), put that in the `tmpfs | overlay` branch
too, see that file for the full pattern.

## A complete example

A recipe that installs `htop` and `ncdu` on Alpine, following the same shape
as the bundled ones:

```sh
#!/bin/sh
# Installs htop and ncdu. Runs as root over ssh on a booted Alpine VM.
set -e

setup-apkrepos -c -1
apk add htop ncdu

root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
    echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot; rebooting will NOT bring it back. Use a disk VM to keep it."
    ;;
*)
    echo "installed on a disk VM (root is $root_fstype): this survives a reboot."
    ;;
esac
```

Save that as `~/.stoat/recipes/tools.alpine.sh` and it shows up in the picker
as `tools` (the picker strips the `.alpine.sh` suffix, see
`internal/tui/labels.go`'s `recipeLabel`) for any Alpine VM on the apkovl or
plain-ssh backend.

## Cloud fragments

A cloud fragment is a `#cloud-config` document, not a shell script. It's
merged into the seed's `user-data` (`internal/cloudinit/cloudinit.go`), which
splices out just the `packages:` and `runcmd:` lists from each selected
fragment and concatenates them: that's the only shape it understands, so stick
to those two top-level keys:

```yaml
#cloud-config
packages:
  - htop
  - ncdu

runcmd:
  - echo "tools installed"
```

Name it `tools.cloud.yaml` for the shared cross-distro fragment, or
`tools.<os>.cloud.yaml` for one that targets a single OS.

### The trap: one fragment doesn't always cover every distro

`packages:` is handed straight to the image's native package manager (`apt`
on Ubuntu/Debian, `pacman` on Arch) with no per-distro syntax. A shared
fragment only works if the package name happens to be spelled identically
everywhere it's offered. `xfce.cloud.yaml` covers Ubuntu, Debian, and Arch
this way because `xfce4` is a real package (or group name) on both apt and
pacman. Fedora broke that: it has no package literally named `xfce4` at all,
the desktop is the comps group `@xfce-desktop-environment`, installed via
`dnf install @xfce-desktop-environment`. Cramming an `@group` token into the
shared fragment would've worked on Fedora and silently failed everywhere else
it's offered, so Fedora gets its own file, `xfce.fedora.cloud.yaml`, and
`recipes.List` offers it *instead of* the shared one for Fedora specifically,
see [the naming table](overview.md#naming-and-how-the-picker-filters).

If you're writing a cross-distro fragment of your own, check every OS you're
offering it to against that OS's actual package manager before assuming one
name works everywhere.

## Tooling for this is still just a proposal

There's no `stoat recipe new` or manifest format yet: writing a file by hand
in `$EDITOR` is the whole workflow today. A design for scaffolding/validating
recipes has been sketched but not built; see
[../recipe-authoring-spec.md](../recipe-authoring-spec.md) for that proposal.
