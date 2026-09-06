#!/bin/sh
# Installs Docker and the compose plugin. Runs as root over ssh on a booted
# Arch VM.
set -e

pacman -Sy --noconfirm docker docker-compose

systemctl enable docker
systemctl start docker

i=0
while [ $i -lt 30 ]; do
    docker info >/dev/null 2>&1 && break
    i=$((i + 1))
    sleep 1
done

docker version --format '{{.Server.Version}}' 2>/dev/null |
    sed 's/^/docker daemon running, version /' ||
    echo "docker installed, but the daemon did not come up: check 'systemctl status docker'"

user="${STOAT_PARAM_USER:?docker.user is unset; run this recipe through stoat apply, not the script directly}"
if ! id "$user" >/dev/null 2>&1; then
    useradd -m -s /bin/bash "$user"
fi
usermod -aG docker "$user"
if [ -n "${STOAT_OUTPUT:-}" ]; then
    printf '%s\n' 'socket=/var/run/docker.sock' >> "$STOAT_OUTPUT"
fi
