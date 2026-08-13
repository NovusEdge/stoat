#!/bin/sh
# Installs Docker and the compose plugin. Runs as root over ssh on a booted
# Fedora VM.
set -e

# Add Docker's official repository
dnf -y install dnf-plugins-core
dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo

dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

systemctl enable docker
systemctl start docker

# Wait for daemon
i=0
while [ $i -lt 30 ]; do
    docker info >/dev/null 2>&1 && break
    i=$((i + 1))
    sleep 1
done

docker version --format '{{.Server.Version}}' 2>/dev/null |
    sed 's/^/docker daemon running, version /' ||
    echo "docker installed, but the daemon did not come up: check 'systemctl status docker'"
