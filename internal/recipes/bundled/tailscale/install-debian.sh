#!/bin/sh
# Installs Tailscale and starts the daemon. Runs as root over ssh on a booted
# Ubuntu or Debian VM.
#
# The auth key is a required secret parameter. It is provided only for this
# invocation, so it never lands in vm.toml or the recipe directory.
set -e

export DEBIAN_FRONTEND=noninteractive

curl -fsSL https://tailscale.com/install.sh | sh

systemctl enable tailscaled
systemctl start tailscaled

# tailscaled needs a moment before `tailscale up` will talk to it
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
