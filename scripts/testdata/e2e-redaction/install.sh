#!/bin/sh
set -eu

test -n "${STOAT_PARAM_TOKEN:?missing redaction token}"
printf 'redaction-secret=%s\n' "$STOAT_PARAM_TOKEN"
printf '%s\n' 'socket=/var/run/redaction.sock' >> "$STOAT_OUTPUT"
