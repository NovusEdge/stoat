#!/usr/bin/env bash
# A stoat built before the provider field must not see a v2 VM. If it lists
# one, `stoat rm` deletes the record while the cloud instance keeps billing.
#
# Before the v2 namespace existed (internal/config/namespace.go), this script
# passed trivially: nothing could create a v2 VM to misread in the first
# place. The meaningful run is against this tree, after that namespace ships.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Pinned rather than "newest tag". v0.4.2 is the last release built before
# config.VM had a provider field, so it is the binary this gate must prove
# blind to a v2 record. Following the newest tag would eventually build a
# stoat that knows about v2, and the gate would keep passing while proving
# nothing.
tag="${STOAT_LEGACY_TAG:-v0.4.2}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

git -C "$repo" worktree add --detach "$work/old" "$tag" >/dev/null 2>&1
(cd "$work/old" && go build -o "$work/stoat-old" ./cmd/stoat)
git -C "$repo" worktree remove --force "$work/old" >/dev/null 2>&1

export STOAT_HOME="$work/home"
mkdir -p "$STOAT_HOME/v2/cloud"
cat >"$STOAT_HOME/v2/cloud/vm.toml" <<'TOML'
name = "cloud"
provider = "gce"
mode = "cloud"
sshport = 2222
TOML

out="$("$work/stoat-old" ls 2>&1 || true)"
if grep -q cloud <<<"$out"; then
  echo "FAIL: stoat $tag listed a v2 VM:"
  echo "$out"
  exit 1
fi

if "$work/stoat-old" rm cloud -y >/dev/null 2>&1; then
  echo "FAIL: stoat $tag deleted a v2 VM"
  exit 1
fi

if [ ! -f "$STOAT_HOME/v2/cloud/vm.toml" ]; then
  echo "FAIL: stoat $tag removed the v2 record"
  exit 1
fi

echo "ok: stoat $tag neither lists nor deletes a v2 VM"
