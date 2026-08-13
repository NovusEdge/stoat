#!/bin/sh
# git, a compiler, an editor and basic fetch tools. Runs as root over ssh on a
# booted Ubuntu or Debian VM.
set -e

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y git curl ca-certificates build-essential vim tmux less bash

git --version
echo "devtools installed: git, curl, build-essential (gcc/make), vim, tmux, less, bash"
