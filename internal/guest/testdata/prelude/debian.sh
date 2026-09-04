stoat_pkg_setup() { apt-get update; }
stoat_pkg_install() { 'apt-get' 'install' '-y' "$@"; }
stoat_svc_enable() { systemctl enable "$1"; }
stoat_svc_start() { systemctl start "$1"; }
stoat_svc_stop() { systemctl stop "$1"; }
stoat_svc_restart() { systemctl restart "$1"; }
stoat_svc_status() { systemctl status "$1"; }
export DEBIAN_FRONTEND='noninteractive'
STOAT_OS=debian; STOAT_INIT=systemd; STOAT_PKGMGR=apt-get
export STOAT_OS STOAT_INIT STOAT_PKGMGR
