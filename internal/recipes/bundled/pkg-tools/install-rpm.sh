#!/bin/sh
# tools for querying the package manager. Runs as root on a booted dnf guest:
# Fedora, AlmaLinux or Rocky.
set -e

stoat_pkg_setup
stoat_pkg_install dnf-utils

manager=$(command -v apt-get 2>/dev/null || command -v dnf 2>/dev/null || command -v zypper 2>/dev/null || command -v pacman 2>/dev/null || command -v apk 2>/dev/null || true)
query_tool=$(command -v repoquery 2>/dev/null || true)
[ -n "$manager" ] || { echo "pkg-tools: no supported package manager found" >&2; exit 1; }
[ -n "$query_tool" ] || { echo "pkg-tools: repoquery was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'manager=%s\n' "$manager"
        printf 'query_tool=%s\n' "$query_tool"
    } >> "$STOAT_OUTPUT"
fi
echo "pkg-tools installed: dnf-utils"
