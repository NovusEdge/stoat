# shared output helpers for justfile recipes. honours NO_COLOR and non-tty.
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  c() { printf '\033[3%sm%s\033[0m' "$1" "$2"; }
else
  c() { printf '%s' "$2"; }
fi
dim()   { if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then printf '\033[2m%s\033[0m\n' "$*"; else printf '%s\n' "$*"; fi; }
head()  { printf '\n  %s %s\n' "$(c 3 "$1")" "$(c 8 "$2")"; printf '  %s\n' "$(c 8 '────────────────────────────────────────────────')"; }
ok()    { printf '  %s %-14s %s\n' "$(c 2 ✓)" "$1" "$(c 8 "$2")"; }
bad()   { printf '  %s %-14s %s\n' "$(c 1 ✗)" "$1" "$2"; }
warn2() { printf '  %s %-14s %s\n' "$(c 3 !)" "$1" "$2"; }
warn()  { printf '  %s %s\n' "$(c 3 !)" "$*"; }
