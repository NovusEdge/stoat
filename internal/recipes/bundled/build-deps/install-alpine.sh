#!/bin/sh
# toolchain for building software from source. Runs as root over ssh on a
# booted Alpine VM.
set -e

# -c enables community (alpine-sdk lives there, outside the base set); -1
# picks a mirror and refreshes indexes, so no separate `apk update`.
# setup-apkrepos runs apk update with no lock-wait, so another apk that holds
# the database lock fails it with exit 99. Retry until the lock frees, up to
# ~60s.
n=0
until setup-apkrepos -c -1; do
    n=$((n + 1))
    [ "$n" -ge 30 ] && { echo "apk database stayed locked; giving up" >&2; exit 1; }
    sleep 2
done

stoat_pkg_install alpine-sdk build-base

compiler=$(command -v cc 2>/dev/null || command -v gcc 2>/dev/null || true)
make_bin=$(command -v make 2>/dev/null || true)
pkg_config=$(command -v pkg-config 2>/dev/null || command -v pkgconf 2>/dev/null || true)
[ -n "$compiler" ] || { echo "build-deps: C compiler was not installed" >&2; exit 1; }
[ -n "$make_bin" ] || { echo "build-deps: make was not installed" >&2; exit 1; }
[ -n "$pkg_config" ] || { echo "build-deps: pkg-config was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'compiler=%s\n' "$compiler"
        printf 'make=%s\n' "$make_bin"
        printf 'pkg_config=%s\n' "$pkg_config"
    } >> "$STOAT_OUTPUT"
fi
echo "build-deps installed: alpine-sdk, build-base"
