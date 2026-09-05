stoat_pkg_setup() { apk update; }
stoat_pkg_install() { 'apk' '--wait' '60' 'add' "$@"; }
stoat_svc_enable() { rc-update add "$1" default; }
stoat_svc_start() { rc-service "$1" start; }
stoat_svc_stop() { rc-service "$1" stop; }
stoat_svc_restart() { rc-service "$1" restart; }
stoat_svc_status() { rc-service "$1" status; }
stoat_download() { wget -O "$@"; }
stoat_useradd() { adduser -D "$1"; }
STOAT_OS=alpine; STOAT_INIT=openrc; STOAT_PKGMGR=apk
export STOAT_OS STOAT_INIT STOAT_PKGMGR
