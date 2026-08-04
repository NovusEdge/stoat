#!/bin/sh
# stoat:name        devtools
# stoat:description git, a compiler, an editor and basic fetch tools
# stoat:os          alpine
# stoat:stages      install
# The baseline you end up installing on every throwaway VM: version control, a
# compiler, an editor, and the tools to fetch things. Runs as root over ssh on
# a booted Alpine VM.
set -e

# -c enables community (where most of these live outside the base set); -1
# picks a mirror and refreshes indexes, so no separate `apk update`.
setup-apkrepos -c -1

# build-base is Alpine's meta-package for gcc/make/libc-dev — the equivalent of
# Debian's build-essential. Alpine has no package called "build-essential".
apk add git curl ca-certificates build-base vim tmux less

# Alpine's default shell is ash and there is no bash unless asked for; scripts
# copied in from elsewhere routinely assume it, so it is part of a baseline.
apk add bash

git --version
echo "devtools installed: git, curl, build-base (gcc/make), vim, tmux, less, bash"

# Live VMs are diskless: the root filesystem is a tmpfs/overlay in RAM, so
# every package installed above is gone on reboot. A disk install mounts a
# real block device as root, which persists. Detecting it from inside the
# guest (rather than assuming) is the same mechanism xfce.alpine.sh uses.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
	echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot — rebooting will NOT bring it back. Use a disk VM to keep it."
	;;
*)
	echo "installed on a disk VM (root is $root_fstype) — this survives a reboot."
	;;
esac
