#!/bin/sh
# tools for querying the package manager. Runs as root on a booted openSUSE
# VM.
set -e

stoat_pkg_setup
stoat_pkg_install zypper libzypp

manager=$(command -v apt-get 2>/dev/null || command -v dnf 2>/dev/null || command -v zypper 2>/dev/null || command -v pacman 2>/dev/null || command -v apk 2>/dev/null || true)
query_tool=$(command -v zypper 2>/dev/null || true)
[ -n "$manager" ] || { echo "pkg-tools: no supported package manager found" >&2; exit 1; }
[ -n "$query_tool" ] || { echo "pkg-tools: zypper was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'manager=%s\n' "$manager"
        printf 'query_tool=%s\n' "$query_tool"
    } >> "$STOAT_OUTPUT"
fi
echo "pkg-tools installed: zypper, libzypp"
