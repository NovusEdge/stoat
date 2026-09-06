#!/bin/sh
set -eu

if [ "${STOAT_EXPECTED_USER:-root}" != root ] && [ "$(id -u)" -eq 0 ]; then
	printf '%s\n' "python fixture ran as root for non-root configured account" >&2
	exit 1
fi
if [ -n "${STOAT_EXPECTED_USER:-}" ] && [ "$(id -un)" != "$STOAT_EXPECTED_USER" ]; then
	printf '%s\n' "python fixture ran as the wrong configured account" >&2
	exit 1
fi

venv=${1:?usage: python-dev.sh VENV_DIR}
python="$venv/bin/python"
[ -x "$python" ]

prefix=$("$python" -c 'import sys; print(sys.prefix)')
base_prefix=$("$python" -c 'import sys; print(sys.base_prefix)')
[ "$prefix" != "$base_prefix" ]
"$python" -m pip --version

system_prefix=$(python3 -c 'import sys; print(sys.prefix)')
[ "$prefix" != "$system_prefix" ]

printf 'python=%s\npython_prefix=%s\nsystem_prefix=%s\n' "$python" "$prefix" "$system_prefix"
