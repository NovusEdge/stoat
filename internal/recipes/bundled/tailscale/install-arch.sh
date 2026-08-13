#!/bin/sh
# Installs Tailscale and starts the daemon. Runs as root over ssh on a booted
# Arch VM.
#
# Does NOT authenticate. Joining a tailnet needs an auth key, and stoat has
# nowhere to keep one safely. This installs and starts the daemon, then tells
# you the one command to run yourself.
set -e

pacman -Sy --noconfirm tailscale

systemctl enable tailscaled
systemctl start tailscaled

# tailscaled needs a moment before `tailscale up` will talk to it
i=0
while [ $i -lt 30 ]; do
    tailscale status >/dev/null 2>&1 && break
    i=$((i + 1))
    sleep 1
done

echo "tailscale installed and tailscaled running."
echo "To join your tailnet, ssh in and run:  tailscale up"
