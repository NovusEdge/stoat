stoat_pkg_setup() { n=0
until setup-apkrepos -c -1; do
    n=$((n + 1))
    [ "$n" -ge 30 ] && { echo "apk database stayed locked; giving up" >&2; exit 1; }
    sleep 2
done; }
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
