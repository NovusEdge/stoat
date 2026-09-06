#!/bin/sh
# toolchain for building software from source. Runs as root on a booted dnf
# guest: Fedora, AlmaLinux or Rocky.
set -e

stoat_pkg_setup
# The group carries the same packages under two ids: dnf5 on Fedora calls it
# development-tools, and dnf4 on AlmaLinux 9 and Rocky 9 calls it development.
# dnf fails with "Module or Group is not available" on the wrong id.
if ! stoat_pkg_install @development-tools; then
    stoat_pkg_install @development
fi
stoat_pkg_install pkgconf-pkg-config rpm-build

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
echo "build-deps installed: the development tools group, pkgconf-pkg-config, rpm-build"
