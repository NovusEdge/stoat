#!/bin/sh
# stoat:name        xfce
# stoat:description XFCE desktop with autologin startx on tty1
# stoat:os          ubuntu
# stoat:requires    systemd
# stoat:stages      install, configure, enable
# Installs XFCE and starts it. Runs as root over ssh on a booted Ubuntu VM.
set -e

apt-get update
apt-get install -y xfce4 xfce4-terminal dbus-x11 xinit xserver-xorg

systemctl enable dbus
systemctl start dbus || true

mkdir -p /mnt/host
mount -a || true

echo 'exec startxfce4' > /root/.xinitrc

# Autologin root on tty1 and start X from the profile: a display manager is
# more machinery than a disposable single-user VM needs.
# ponytail: no DM. If you ever want multi-user or a login screen, that is when to add one.
#
# Ubuntu's tty1 is managed by systemd's getty@.service, so autologin is a
# drop-in override rather than an /etc/inittab edit (that's the alpine/
# busybox mechanism, not systemd's). agetty's --autologin does the same job
# busybox getty's `-n -l LOGIN` trick does on Alpine.
mkdir -p /etc/systemd/system/getty@tty1.service.d
cat > /etc/systemd/system/getty@tty1.service.d/autologin.conf <<'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
EOF
systemctl daemon-reload
systemctl enable getty@tty1.service

# Guarded so re-running the recipe doesn't stack multiple "exec startx"
# blocks in .profile. exec matters: without it, quitting X drops to a shell
# that immediately re-runs the profile and restarts X. The DISPLAY check
# stops a recursive startx if .profile is sourced from inside the X session.
if ! grep -q 'exec startx' /root/.profile 2>/dev/null; then
    cat >> /root/.profile <<'EOF'
if [ "$(tty)" = "/dev/tty1" ] && [ -z "$DISPLAY" ]; then
    exec startx
fi
EOF
fi

# Live vs disk: an ssh-reachable Ubuntu VM is not always a normal disk
# install — a live/installer session also mounts its root as tmpfs/overlay,
# and everything written above (the getty override, /root/.profile, the
# installed packages) lives only in that RAM-backed root and is gone on
# reboot. Same detection as the alpine recipe: inspect the mounted root
# filesystem type via /proc/mounts.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
    # Live root: apply the getty change to the running session right now
    # instead of promising a reboot that would wipe it.
    systemctl restart getty@tty1.service || true
    echo "xfce installed and starting NOW on the vm's console (this session only) — a live root keeps nothing across reboots (root is tmpfs/overlay, wiped on restart), so rebooting will NOT bring xfce back. For a desktop that survives reboots, provision a disk-installed VM instead."
    ;;
*)
    echo "xfce installed — reboot the vm to land in xfce automatically (tty1 now autologins root and starts X on next boot)"
    ;;
esac
