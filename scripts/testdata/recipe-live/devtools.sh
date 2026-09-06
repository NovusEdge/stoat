#!/bin/sh
set -eu

if [ "${STOAT_EXPECTED_USER:-root}" != root ] && [ "$(id -u)" -eq 0 ]; then
	printf '%s\n' "devtools fixture ran as root for non-root configured account" >&2
	exit 1
fi
if [ -n "${STOAT_EXPECTED_USER:-}" ] && [ "$(id -un)" != "$STOAT_EXPECTED_USER" ]; then
	printf '%s\n' "devtools fixture ran as the wrong configured account" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

git -C "$tmp" init -q
git -C "$tmp" config user.email stoat-fixture@example.invalid
git -C "$tmp" config user.name stoat-fixture

cat > "$tmp/main.c" <<'EOF'
#include <stdio.h>

int main(void) {
    puts("stoat-devtools-ok");
    return 0;
}
EOF

cc "$tmp/main.c" -o "$tmp/main"
[ "$("$tmp/main")" = stoat-devtools-ok ]

git --version
cc --version | head -n 1
command -v cc
command -v vim
