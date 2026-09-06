#!/bin/sh
# git, a compiler, an editor and basic fetch tools. Runs as root over ssh on a
# booted Ubuntu or Debian VM.
set -e

export DEBIAN_FRONTEND=noninteractive
stoat_pkg_setup
stoat_pkg_install git curl ca-certificates build-essential vim tmux less bash

git --version
compiler=$(command -v cc 2>/dev/null || command -v gcc 2>/dev/null || true)
editor=$(command -v vim 2>/dev/null || true)
git_path=$(command -v git 2>/dev/null || true)
[ -n "$compiler" ] || { echo "devtools: C compiler was not installed" >&2; exit 1; }
[ -n "$editor" ] || { echo "devtools: editor was not installed" >&2; exit 1; }
[ -n "$git_path" ] || { echo "devtools: git was not installed" >&2; exit 1; }
if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'compiler=%s\n' "$compiler"
        printf 'editor=%s\n' "$editor"
        printf 'git=%s\n' "$git_path"
    } >> "$STOAT_OUTPUT"
fi
echo "devtools installed: git, curl, build-essential (gcc/make), vim, tmux, less, bash"
