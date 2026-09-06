#!/bin/sh
# tools for querying the package manager. Runs as root over ssh on a booted
# Alpine VM.
set -e

# -1 picks a mirror and refreshes indexes, so no separate stoat_pkg_setup call
# is needed here. -c is kept even though apk-tools lives in Alpine's main
# repository, so the script does not depend on which repo a package happens
# to live in today. setup-apkrepos runs apk update with no lock-wait, so
# another apk that holds the database lock fails it with exit 99. Retry until
# the lock frees, up to ~60s.
n=0
until setup-apkrepos -c -1; do
    n=$((n + 1))
    [ "$n" -ge 30 ] && { echo "apk database stayed locked; giving up" >&2; exit 1; }
    sleep 2
done

stoat_pkg_install apk-tools

manager=$(command -v apt-get 2>/dev/null || command -v dnf 2>/dev/null || command -v zypper 2>/dev/null || command -v pacman 2>/dev/null || command -v apk 2>/dev/null || true)
query_tool=$(command -v apk 2>/dev/null || true)
[ -n "$manager" ] || { echo "pkg-tools: no supported package manager found" >&2; exit 1; }
[ -n "$query_tool" ] || { echo "pkg-tools: apk was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'manager=%s\n' "$manager"
        printf 'query_tool=%s\n' "$query_tool"
    } >> "$STOAT_OUTPUT"
fi
echo "pkg-tools installed: apk-tools"
