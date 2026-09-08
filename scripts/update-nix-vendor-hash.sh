#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 [--check] [--flake PATH]" >&2
}

check_only=false
flake=flake.nix
while (($#)); do
  case $1 in
    --check) check_only=true ;;
    --flake)
      (($# >= 2)) || { usage; exit 2; }
      flake=$2
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
  shift
done

[[ -f $flake ]] || { echo "flake not found: $flake" >&2; exit 2; }

assignment_count=$(awk '
  { sub(/[[:space:]]*#.*/, ""); count += gsub(/vendorHash[[:space:]]*=/, "&") }
  END { print count + 0 }
' "$flake")
if [[ $assignment_count != 1 ]]; then
  echo "expected exactly one vendorHash assignment in $flake" >&2
  exit 2
fi

hash_line_re='^[[:space:]]*vendorHash[[:space:]]*=[[:space:]]*"sha256-[A-Za-z0-9+/]{43}="[[:space:]]*;[[:space:]]*$'
if ! grep -Eq "$hash_line_re" "$flake"; then
  echo "vendorHash assignment is missing or malformed in $flake" >&2
  exit 2
fi
current_hash=$(sed -nE 's/^[[:space:]]*vendorHash[[:space:]]*=[[:space:]]*"(sha256-[A-Za-z0-9+\/]{43}=)"[[:space:]]*;[[:space:]]*$/\1/p' "$flake")

flake_dir=$(cd "$(dirname "$flake")" && pwd)
flake_name=$(basename "$flake")
output=$(mktemp)
trap 'rm -f "$output"' EXIT
set +e
(
  cd "$flake_dir"
  nix build --impure --expr '(builtins.getFlake ("git+file://"+toString ./.)).packages.x86_64-linux.default.goModules.overrideAttrs (_: { outputHash=""; outputHashAlgo="sha256"; })'
) >"$output" 2>&1
build_status=$?
set -e

if ((build_status == 0)); then
  echo 'nix build unexpectedly succeeded; refusing to update vendorHash' >&2
  exit 1
fi

mismatch_count=$(grep -F -c 'hash mismatch in fixed-output derivation' "$output" || true)
got_count=$(grep -Eo 'got:[[:space:]]*' "$output" | wc -l | tr -d ' ')
got_hash=$(grep -Eo 'got:[[:space:]]*sha256-[A-Za-z0-9+/]{43}=' "$output" | sed -E 's/^got:[[:space:]]*//' || true)
valid_got_count=$(printf '%s\n' "$got_hash" | sed '/^$/d' | wc -l | tr -d ' ')
if [[ $mismatch_count != 1 || $got_count != 1 || $valid_got_count != 1 ]]; then
  echo 'nix build did not report exactly one expected vendor hash mismatch' >&2
  cat "$output" >&2
  exit 1
fi
if [[ $current_hash == "$got_hash" ]]; then
  echo "$flake_name vendorHash is up to date"
  exit 0
fi
if $check_only; then
  echo "$flake_name vendorHash is stale (expected $got_hash)" >&2
  exit 1
fi

updated=$(mktemp)
trap 'rm -f "$output" "$updated"' EXIT
awk -v new_hash="$got_hash" '
  /^[[:space:]]*vendorHash[[:space:]]*=/ {
    sub(/"sha256-[A-Za-z0-9+\/]+=*"/, "\"" new_hash "\"")
  }
  { print }
' "$flake" >"$updated"
chmod --reference="$flake" "$updated"
mv "$updated" "$flake"
echo "updated $flake vendorHash to $got_hash"
