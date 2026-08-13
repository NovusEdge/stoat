#!/bin/sh
# git, a compiler, an editor and basic fetch tools. Runs as root over ssh on a
# booted Fedora VM.
set -e

dnf install -y git curl ca-certificates gcc make vim tmux less bash

git --version
echo "devtools installed: git, curl, gcc, make, vim, tmux, less, bash"
