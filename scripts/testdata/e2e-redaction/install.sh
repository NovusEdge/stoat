#!/bin/sh
set -eu

test -n "${STOAT_PARAM_TOKEN:?missing redaction token}"
printf '%s\n' 'socket=/var/run/redaction.sock' >> "$STOAT_OUTPUT"
