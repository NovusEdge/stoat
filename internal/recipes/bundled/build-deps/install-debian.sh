#!/bin/sh
# toolchain for building software from source. Runs as root over ssh on a
# booted Ubuntu or Debian VM.
set -e

export DEBIAN_FRONTEND=noninteractive
stoat_pkg_setup
stoat_pkg_install build-essential pkg-config autoconf automake libtool dpkg-dev

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
echo "build-deps installed: build-essential, pkg-config, autoconf, automake, libtool, dpkg-dev"
