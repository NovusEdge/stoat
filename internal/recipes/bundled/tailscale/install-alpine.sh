#!/bin/sh
# Installs Tailscale and starts the daemon. Runs as root over ssh on a booted
# Alpine VM.
#
# It deliberately does NOT authenticate. Joining a tailnet needs an auth key,
# and stoat has nowhere to keep one that isn't worse than the alternative: a
# key in vm.toml would sit in plaintext in the data root, and a key baked into
# a recipe would end up in git. So this installs and starts the daemon, then
# tells you the one command to run yourself.
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

echo "tailscale installed and tailscaled running."
echo "To join your tailnet, ssh in and run:  tailscale up"
echo "(stoat does not store auth keys, see this recipe's header for why.)"

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
