stoat_pkg_setup() { pacman -Sy --noconfirm; }
stoat_pkg_install() { 'pacman' '-S' '--noconfirm' "$@"; }
stoat_svc_enable() { systemctl enable "$1"; }
stoat_svc_start() { systemctl start "$1"; }
stoat_svc_stop() { systemctl stop "$1"; }
stoat_svc_restart() { systemctl restart "$1"; }
stoat_svc_status() { systemctl status "$1"; }
STOAT_OS=arch; STOAT_INIT=systemd; STOAT_PKGMGR=pacman
export STOAT_OS STOAT_INIT STOAT_PKGMGR
