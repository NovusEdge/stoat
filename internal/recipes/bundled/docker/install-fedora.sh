#!/bin/sh
# Installs Docker and the compose plugin. Runs as root over ssh on a booted
# Fedora VM.
set -e

# dnf5 (Fedora 41+) dropped `config-manager --add-repo`. Write the repo file
# directly; that works on both dnf4 and dnf5.
curl -fsSL https://download.docker.com/linux/fedora/docker-ce.repo \
    -o /etc/yum.repos.d/docker-ce.repo

dnf install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

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
