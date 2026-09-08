#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
script=$repo_root/scripts/update-nix-vendor-hash.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

assert_eq() {
  if [[ $1 != "$2" ]]; then
    printf 'assertion failed: expected %q, got %q\n' "$1" "$2" >&2
    exit 1
  fi
}

make_fixture() {
  local name=$1
  local output=$2
  local status=$3
  local fixture=$test_root/$name
  mkdir -p "$fixture/bin"
  cat >"$fixture/flake.nix" <<'EOF'
vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
EOF
  cat >"$fixture/bin/nix" <<EOF
#!/usr/bin/env bash
printf '%b\\n' '$output' >&2
exit $status
EOF
  chmod +x "$fixture/bin/nix"
  printf '%s\n' "$fixture"
}

valid_hash='sha256-0123456789abcdefghijklmnopqrstuvABCDEFGHIJK='
fixture=$(make_fixture valid "hash mismatch in fixed-output derivation\ngot: $valid_hash" 1)
PATH="$fixture/bin:$PATH" "$script" --flake "$fixture/flake.nix"
grep -Fq "vendorHash = \"$valid_hash\";" "$fixture/flake.nix"

fixture=$(make_fixture network 'error: unable to download source: connection timed out' 1)
if PATH="$fixture/bin:$PATH" "$script" --flake "$fixture/flake.nix"; then
  echo 'network failure unexpectedly succeeded' >&2
  exit 1
fi

fixture=$(make_fixture malformed 'hash mismatch in fixed-output derivation\ngot: not-a-sha256-hash' 1)
if PATH="$fixture/bin:$PATH" "$script" --flake "$fixture/flake.nix"; then
  echo 'malformed hash unexpectedly succeeded' >&2
  exit 1
fi

fixture=$(make_fixture ambiguous "hash mismatch in fixed-output derivation\ngot: $valid_hash\ngot: sha256-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=" 1)
if PATH="$fixture/bin:$PATH" "$script" --flake "$fixture/flake.nix"; then
  echo 'ambiguous hash unexpectedly succeeded' >&2
  exit 1
fi

fixture=$(make_fixture noop "hash mismatch in fixed-output derivation\ngot: sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" 1)
before=$(<"$fixture/flake.nix")
PATH="$fixture/bin:$PATH" "$script" --flake "$fixture/flake.nix"
after=$(<"$fixture/flake.nix")
assert_eq "$before" "$after"

fixture=$(make_fixture check "hash mismatch in fixed-output derivation\ngot: $valid_hash" 1)
if PATH="$fixture/bin:$PATH" "$script" --check --flake "$fixture/flake.nix"; then
  echo 'stale --check unexpectedly succeeded' >&2
  exit 1
fi
grep -Fq 'sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=' "$fixture/flake.nix"

echo 'update-nix-vendor-hash tests passed'
