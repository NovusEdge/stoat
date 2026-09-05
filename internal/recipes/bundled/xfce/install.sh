#!/bin/sh
# Installs XFCE and starts it. Runs as root over ssh on a booted VM.
set -e

stoat_pkg_setup

case "$STOAT_PKGMGR" in
apk)
    # Xorg reads its input devices from udev. Alpine boots mdev by default,
    # which never feeds Xorg, so libinput finds no mouse or keyboard and the
    # desktop ignores every click. setup-devd switches to eudev and adds it
    # to sysinit for the next boot; udevadm trigger/settle cold-plugs the
    # devices that already exist so this boot sees them too.
    setup-devd udev
    udevadm trigger 2>/dev/null || true
    udevadm settle 2>/dev/null || true
    # Alpine's xfce4 metapackage pulls no X server. setup-xorg-base installs
    # xorg-server, xinit and xf86-input-libinput, which startx below needs.
    setup-xorg-base
    stoat_pkg_install xfce4 xfce4-terminal dbus-x11
    ;;
apt-get)
    stoat_pkg_install xfce4 xfce4-terminal dbus-x11 xinit xserver-xorg
    ;;
pacman)
    stoat_pkg_install xfce4 xfce4-terminal dbus xorg-xinit
    ;;
esac

stoat_svc_enable dbus
stoat_svc_start dbus || true

mkdir -p /mnt/host
mount -a || true

echo 'exec startxfce4' > /root/.xinitrc

# Autologin root on tty1 and start X from the profile: a display manager is
# more machinery than a disposable single-user VM needs.
# ponytail: no DM. If you ever want multi-user or a login screen, that is when to add one.
case "$STOAT_INIT" in
openrc)
    # busybox getty (Alpine, not util-linux) has no -a/autologin flag: the
    # busybox-native trick is `getty -n -l LOGIN`, invoking LOGIN directly in
    # place of /bin/login. LOGIN runs `login -f root` (busybox login's
    # "-f: don't authenticate, user already authenticated" flag), so a real
    # login session starts and sources /root/.profile.
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
    ;;
systemd)
    # tty1 is managed by systemd's getty@.service, so autologin is a
    # drop-in override rather than an inittab edit.
    mkdir -p /etc/systemd/system/getty@tty1.service.d
    cat > /etc/systemd/system/getty@tty1.service.d/autologin.conf <<'EOF'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
EOF
    systemctl daemon-reload
    systemctl enable getty@tty1.service
    ;;
esac

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

# Live vs disk: an ssh-reachable VM is not always a normal disk install: a
# live/installer session also mounts its root as tmpfs/overlay, and
# everything written above (the getty config, /root/.profile, the installed
# packages) lives only in that RAM-backed root and is gone on reboot.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
    case "$STOAT_INIT" in
    openrc)
        # busybox init documents `HUP: reload /etc/inittab` in its own
        # --help text; reloading inittab makes it respawn tty1 through the
        # new command without a reboot, so XFCE comes up on the VM's
        # console this boot.
        kill -HUP 1
        ;;
    systemd)
        systemctl restart getty@tty1.service || true
        ;;
    esac
    echo "xfce installed and starting NOW on the vm's console (this session only). A live root keeps nothing across reboots (root is tmpfs/overlay, wiped on restart), so rebooting will NOT bring xfce back. For a desktop that survives reboots, provision a disk-installed VM instead."
    ;;
*)
    echo "xfce installed. Reboot the vm to land in xfce automatically (tty1 now autologins root and starts X on next boot)"
    ;;
esac
