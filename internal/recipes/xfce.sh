#!/bin/sh
# Installs XFCE and starts it. Runs as root over ssh, on a booted Alpine live VM.
set -e

setup-apkrepos -c -1
apk update

setup-xorg-base
apk add xfce4 xfce4-terminal dbus-x11

rc-update add dbus
rc-service dbus start || true

mkdir -p /mnt/host
mount -a || true

echo 'exec startxfce4' > /root/.xinitrc

# Autologin root on tty1 and start X from the profile: a display manager is
# more machinery than a disposable single-user VM needs.
# ponytail: no DM. If you ever want multi-user or a login screen, that is when to add one.
#
# busybox getty (this is Alpine, not util-linux) has NO -a/autologin flag —
# verified against the actual busybox 1.37.0-r31 shipped on the Alpine 3.24.1
# ISO, whose `getty --help` lists only -h -L -m -n -w -i -f -l -t -I -H. The
# busybox-native trick is `getty -n -l LOGIN`: -n skips the username prompt
# and LOGIN is invoked directly in place of /bin/login. LOGIN then runs
# `login -f root` (busybox login's "-f: don't authenticate, user already
# authenticated" flag) so a real login session starts — MOTD, utmp entry,
# and a proper login shell that sources /root/.profile.
cat > /sbin/autologin <<'EOF'
#!/bin/sh
exec /bin/login -f root
EOF
chmod +x /sbin/autologin

sed -i 's|^tty1::respawn:.*|tty1::respawn:/sbin/getty -n -l /sbin/autologin 38400 tty1|' /etc/inittab
grep -q '^tty1::respawn:/sbin/getty -n -l /sbin/autologin' /etc/inittab || {
    echo "failed to configure tty1 autologin in /etc/inittab" >&2
    exit 1
}

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

echo "xfce installed — reboot the vm to land in xfce automatically (tty1 now autologins root and starts X on next boot)"
