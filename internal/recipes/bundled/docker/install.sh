#!/bin/sh
# Installs Docker and the compose plugin. Default script for OSes not explicitly
# listed in [scripts]. Assumes apt-get and systemd (Debian-family).
set -e

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y ca-certificates curl gnupg

install -m 0755 -d /etc/apt/keyrings
id=$(. /etc/os-release && echo "$ID")
curl -fsSL "https://download.docker.com/linux/$id/gpg" | \
    gpg --batch --yes --no-tty --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg

echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
https://download.docker.com/linux/$(. /etc/os-release && echo "$ID") \
$(. /etc/os-release && echo "$VERSION_CODENAME") stable" | \
    tee /etc/apt/sources.list.d/docker.list > /dev/null

apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

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
