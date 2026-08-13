#!/bin/sh
# git, a compiler, an editor and basic fetch tools. Runs as root over ssh on a
# booted Arch VM.
set -e

pacman -Sy --noconfirm git curl ca-certificates base-devel vim tmux less bash

git --version
echo "devtools installed: git, curl, base-devel (gcc/make), vim, tmux, less, bash"
