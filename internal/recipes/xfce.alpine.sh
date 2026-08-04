#!/bin/sh
# stoat:name        xfce
# stoat:description XFCE desktop with autologin startx on tty1
# stoat:os          alpine
# stoat:stages      install, configure, enable
# Installs XFCE and starts it. Runs as root over ssh, on a booted Alpine live VM.
set -e

# setup-apkrepos -c -1 already refreshes the indexes ("Updating repository
# indexes... done"), so a separate `apk update` here is redundant work that
# only widens the window for a transient ssh/network drop under `set -e` to
# kill the whole provision. Don't add retries either: a real apk failure
# should still fail loudly.
setup-apkrepos -c -1

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
# busybox getty (this is Alpine, not util-linux) has NO -a/autologin flag,
# confirmed against the actual busybox 1.37.0-r31 shipped on the Alpine 3.24.1
# ISO, whose `getty --help` lists only -h -L -m -n -w -i -f -l -t -I -H. The
# busybox-native trick is `getty -n -l LOGIN`: -n skips the username prompt
# and LOGIN is invoked directly in place of /bin/login. LOGIN then runs
# `login -f root` (busybox login's "-f: don't authenticate, user already
# authenticated" flag) so a real login session starts: MOTD, utmp entry,
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

# Live vs disk: a live VM boots Alpine's diskless mode, whose initramfs
# mounts the root filesystem as tmpfs (and, on the -o overlaytmpfs boot
# option, overlay-on-tmpfs), confirmed against
# /usr/share/mkinitfs/initramfs-init from mkinitfs-3.14.0-r0 (the package
# apk installs from the Alpine 3.24 repos): the diskless path reads
# `mount -t tmpfs -o "$rootflags" tmpfs "$sysroot"` and the overlaytmpfs
# path reads `mount -t overlay -o ... overlayfs "$sysroot"`. Everything
# written there (/etc/inittab, /root/.profile, the installed packages)
# lives in RAM and is gone on reboot; only the apkovl (which cannot carry
# installed packages) survives. A disk install (setup-disk) instead mounts
# a real block-device filesystem (ext4 by default) as root, which persists.
# So: root mounted tmpfs/overlay == live/diskless, anything else == disk.
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)

case "$root_fstype" in
tmpfs | overlay)
    # Live VM: the autologin/.profile edits above are still worth having in
    # case tty1 gets re-inited some other way, but they will NOT survive a
    # reboot, so telling the user to reboot for a desktop would be a lie.
    # Instead, get XFCE onto the VM's own console *right now*, in this
    # boot: busybox init (this is Alpine, not sysvinit) documents `HUP:
    # reload /etc/inittab` in its own --help text, and reloading inittab is
    # the standard way to make init respawn a tty entry whose command
    # changed without a reboot. Sending it makes init respawn tty1 through
    # /sbin/autologin -> login -f root -> .profile -> exec startx, so XFCE
    # comes up on the VM's console (the one qemu displays) without the user
    # touching the console at all.
    kill -HUP 1
    echo "xfce installed and starting NOW on the vm's console (this session only). A live VM keeps nothing across reboots (root is tmpfs/overlay, wiped on restart), so rebooting will NOT bring xfce back. For a desktop that survives reboots, provision a disk VM instead."
    ;;
*)
    echo "xfce installed. Reboot the vm to land in xfce automatically (tty1 now autologins root and starts X on next boot)"
    ;;
esac
