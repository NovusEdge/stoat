#!/bin/sh
# tools for inspecting services and the processes behind them. Runs as root
# over ssh on a booted Arch VM.
set -e

stoat_pkg_setup
stoat_pkg_install lsof strace procps-ng

service_manager=""
if command -v systemctl >/dev/null 2>&1; then
    service_manager=systemd
elif command -v rc-status >/dev/null 2>&1; then
    service_manager=openrc
fi
lsof_bin=$(command -v lsof 2>/dev/null || true)
strace_bin=$(command -v strace 2>/dev/null || true)
[ -n "$service_manager" ] || { echo "service-tools: no known service manager (systemd or openrc) was found" >&2; exit 1; }
[ -n "$lsof_bin" ] || { echo "service-tools: lsof was not installed" >&2; exit 1; }
[ -n "$strace_bin" ] || { echo "service-tools: strace was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'service_manager=%s\n' "$service_manager"
        printf 'lsof=%s\n' "$lsof_bin"
        printf 'strace=%s\n' "$strace_bin"
    } >> "$STOAT_OUTPUT"
fi
echo "service-tools installed: lsof, strace, procps-ng"
