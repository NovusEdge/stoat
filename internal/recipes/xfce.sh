#!/bin/sh
# Installs XFCE and starts it. Runs as root over ssh, on a booted Alpine live VM.
set -e

setup-apkrepos -c -1
apk update

setup-xorg-base
apk add xfce4 xfce4-terminal dbus-x11 xf86-video-qxl

rc-update add dbus
rc-service dbus start || true

mkdir -p /mnt/host
mount -a || true

echo 'exec startxfce4' > /root/.xinitrc
echo "xfce installed — run startx on the vm console"
