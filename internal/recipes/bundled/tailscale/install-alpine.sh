#!/bin/sh
# Installs Tailscale and starts the daemon. Runs as root over ssh on a booted
# Alpine VM.
#
# The auth key is a required secret parameter. It is provided only for this
# invocation, so it never lands in vm.toml or the recipe directory.
set -e

# -c enables community, where tailscale lives; -1 picks a mirror and refreshes
# indexes, so no separate `apk update`.
# setup-apkrepos runs apk update with no lock-wait, so another apk that holds
# the database lock fails it with exit 99. Retry until the lock frees, up to
# ~60s.
n=0
until setup-apkrepos -c -1; do
    n=$((n + 1))
    [ "$n" -ge 30 ] && { echo "apk database stayed locked; giving up" >&2; exit 1; }
    sleep 2
done

# --wait 60 makes apk wait up to 60s for the lock instead of failing with
# exit 99 when another apk run holds it.
apk --wait 60 add tailscale

rc-update add tailscale default
rc-service tailscale start

# tailscaled needs a moment before `tailscale up` will talk to it.
i=0
while [ $i -lt 30 ]; do
	tailscale status >/dev/null 2>&1 && break
	i=$((i + 1))
	sleep 1
done

if [ -n "${STOAT_PARAM_AUTHKEY:-}" ]; then
	tailscale up --authkey "$STOAT_PARAM_AUTHKEY"
fi
echo "tailscale installed and tailscaled running."

# Live VMs are diskless: the root filesystem is a tmpfs/overlay in RAM, so
# every package installed above is gone on reboot. A disk install mounts a
# real block device as root, which persists. Detecting it from inside the
# guest (rather than assuming) is the same mechanism xfce.alpine.sh uses.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
	echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot. Rebooting will NOT bring it back. Use a disk VM to keep it."
	;;
*)
	echo "installed on a disk VM (root is $root_fstype), so this survives a reboot."
	;;
esac
