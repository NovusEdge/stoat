stoat_pkg_setup() { zypper --non-interactive refresh; }
stoat_pkg_install() { 'zypper' '--non-interactive' 'install' "$@"; }
stoat_svc_enable() { systemctl enable "$1"; }
stoat_svc_start() { systemctl start "$1"; }
stoat_svc_stop() { systemctl stop "$1"; }
stoat_svc_restart() { systemctl restart "$1"; }
stoat_svc_status() { systemctl status "$1"; }
stoat_download() { curl -fsSL -o "$@"; }
stoat_useradd() { useradd -m -s /bin/bash "$1"; }
STOAT_OS=opensuse; STOAT_INIT=systemd; STOAT_PKGMGR=zypper
export STOAT_OS STOAT_INIT STOAT_PKGMGR
