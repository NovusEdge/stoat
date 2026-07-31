#!/bin/sh
# Installs Docker and the compose plugin. Runs as root over ssh on a booted
# Alpine VM.
set -e

# -c enables the community repository, which is where docker lives (main has
# no docker package at all). -1 picks the fastest mirror and refreshes the
# indexes, so a separate `apk update` would be redundant work that only widens
# the window for a transient network drop to kill the run under `set -e`.
setup-apkrepos -c -1

# Verified against pkgs.alpinelinux.org: both are in community, and compose is
# a SEPARATE package from docker (docker alone gives you no `docker compose`).
apk add docker docker-cli-compose

rc-update add docker default
rc-service docker start

# The daemon takes a moment to open its socket; without this the very next
# command a user types fails with "Cannot connect to the Docker daemon" on a
# machine where docker is in fact fine.
i=0
while [ $i -lt 30 ]; do
	docker info >/dev/null 2>&1 && break
	i=$((i + 1))
	sleep 1
done

docker version --format '{{.Server.Version}}' 2>/dev/null |
	sed 's/^/docker daemon running, version /' ||
	echo "docker installed, but the daemon did not come up — check 'rc-service docker status'"

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
