#!/usr/bin/env sh
# End-to-end lifecycle check: one `stoat up` must take a fresh Alpine disk VM
# from nothing to a desktop with a working mouse, with zero manual steps.
#
# The chain under test: install -> auto-restart after poweroff -> auto-apply
# the xfce recipe -> reboot once so eudev/libinput take effect. Each stage is
# code that has regressed before, so this asserts the OUTCOME a user sees:
# udev is the device manager and Xorg drives input through libinput.
#
# Runs against a temporary data root by default, under a unique VM name it
# deletes on exit. Set STOAT_HOME to retain the VM directory for inspection.
# Needs KVM and network; the xfce apk pull is ~1.4GB, so budget ~15 minutes.
set -eu

IMAGE=alpine-standard
RECIPE=xfce
VM="e2e-$$"
UP_TIMEOUT=1200   # install + xfce pull + reboot-once
X_TIMEOUT=90      # Xorg restart after the reboot-once
E2E_RAM=${STOAT_E2E_RAM:-2048}
E2E_CPUS=${STOAT_E2E_CPUS:-2}
E2E_DISPLAY=${STOAT_E2E_DISPLAY:-vnc}

# Prefer the just-built binary over whatever is on PATH.
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
STOAT="$root/stoat"
[ -x "$STOAT" ] || STOAT=stoat

say() { printf '\n=== %s ===\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }

capture_failure_evidence() {
	evidence=${STOAT_E2E_EVIDENCE_DIR:-${TMPDIR:-/tmp}/stoat-e2e-evidence-$VM}
	if ! mkdir -p "$evidence"; then
		printf 'could not create failure evidence directory: %s\n' "$evidence" >&2
		return 1
	fi
	if ! "$STOAT" screenshot "$VM" -o "$evidence/screenshot.png" >"$evidence/screenshot.txt" 2>&1; then
		printf 'screenshot unavailable; see %s/screenshot.txt\n' "$evidence" >&2
	fi
	if ! "$STOAT" logs "$VM" --which console >"$evidence/console.log" 2>&1; then
		printf 'console log capture failed; see %s/console.log\n' "$evidence" >&2
	fi
	if ! "$STOAT" logs "$VM" --which apply >"$evidence/provision.log" 2>&1; then
		printf 'provision log capture failed; see %s/provision.log\n' "$evidence" >&2
	fi
	printf 'failure evidence retained at %s\n' "$evidence" >&2
}

cleanup() {
	status=$?
	if [ -z "${STOAT_HOME:-}" ] || [ ! -f "$STOAT_HOME/$VM/vm.toml" ]; then
		exit "$status"
	fi
	trap - EXIT INT TERM
	if [ "$status" -ne 0 ]; then
		capture_failure_evidence || true
	fi
	cleanup_status=0
	if [ -f "$STOAT_HOME/$VM/qemu.pid" ] && ! "$STOAT" down "$VM"; then
		printf 'cleanup failed: %s is still running; preserving %s for evidence\n' "$VM" "$STOAT_HOME/$VM" >&2
		cleanup_status=1
	fi
	if [ "$cleanup_status" -eq 0 ] && ! "$STOAT" rm "$VM" -y; then
		printf 'cleanup failed: could not delete %s; preserving %s for evidence\n' "$VM" "$STOAT_HOME/$VM" >&2
		cleanup_status=1
	fi
	if [ "$cleanup_status" -ne 0 ] && [ "$status" -eq 0 ]; then
		status=$cleanup_status
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

if [ -z "${STOAT_HOME:-}" ]; then
	STOAT_HOME=$(mktemp -d "${TMPDIR:-/tmp}/stoat-e2e.XXXXXX")
fi
export STOAT_HOME

case "$E2E_RAM" in
	''|*[!0-9]*) fail "STOAT_E2E_RAM must be an integer no greater than 2048";;
esac
case "$E2E_CPUS" in
	''|*[!0-9]*) fail "STOAT_E2E_CPUS must be an integer no greater than 2";;
esac
[ "$E2E_RAM" -ge 256 ] && [ "$E2E_RAM" -le 2048 ] || fail "STOAT_E2E_RAM must be between 256 and 2048 MiB"
[ "$E2E_CPUS" -ge 1 ] && [ "$E2E_CPUS" -le 2 ] || fail "STOAT_E2E_CPUS must be between 1 and 2"
[ "$E2E_DISPLAY" = vnc ] || fail "STOAT_E2E_DISPLAY must be vnc"
export STOAT_GRAPHICAL=0

say "build"
( cd "$root" && go build -o stoat ./cmd/stoat )

say "image $IMAGE"
"$STOAT" images 2>/dev/null | grep -q "^$IMAGE .*downloaded" || "$STOAT" pull "$IMAGE"

say "create $VM (alpine disk, recipe $RECIPE)"
"$STOAT" create "$VM" --image "$IMAGE" --mode disk --ram "$E2E_RAM" --cpus "$E2E_CPUS" --recipes "$RECIPE"

say "up $VM (install -> restart -> apply -> reboot-once)"
timeout "$UP_TIMEOUT" "$STOAT" up "$VM" || fail "up did not finish in ${UP_TIMEOUT}s"

say "assert: disk installer completed and wrote an ext4 root"
"$STOAT" exec "$VM" -- sh -c 'test -s /mnt/work/.installed' \
	|| fail "disk installer completion marker is missing"

grep -Fq 'stoat: install complete, powering off' "$STOAT_HOME/$VM/console.log" \
	|| fail "console.log has no install completion message"

"$STOAT" exec "$VM" -- sh -c "[ \"\$(awk '\$2 == \"/\" { print \$3 }' /proc/mounts)\" = ext4 ]" \
	|| fail "installed guest root is not ext4"

say "assert: recipe applied (xfce present)"
"$STOAT" exec "$VM" -- sh -c 'command -v xfce4-session' \
	|| fail "xfce4-session missing: the recipe did not apply"

say "assert: udev is the device manager (not mdev)"
"$STOAT" exec "$VM" -- sh -c 'pgrep -x udevd >/dev/null' \
	|| fail "udevd not running: setup-devd udev did not take effect"
"$STOAT" exec "$VM" -- sh -c '! pgrep -x mdev >/dev/null' \
	|| fail "mdev still running: the device manager did not switch to udev"

say "assert: Xorg drives input through libinput (mouse clickable)"
# X restarts on the reboot-once; poll until its log records a libinput device.
end=0
found=0
while [ "$found" -eq 0 ] && [ "$end" -lt "$X_TIMEOUT" ]; do
	if "$STOAT" exec "$VM" -- sh -c \
		"grep -q \"Using input driver 'libinput'\" /var/log/Xorg.0.log 2>/dev/null"; then
		found=1
		printf '\nPASS: %s reached a clickable xfce desktop with no manual steps\n' "$VM"
		break
	fi
	end=$((end + 5))
	sleep 5
done
[ "$found" -eq 1 ] || fail "no libinput device in Xorg.0.log after ${X_TIMEOUT}s"

E2E_SECRET="stoat-e2e-secret-$$"
export STOAT_SECRET_REDACTION_TOKEN=$E2E_SECRET
redaction_src="$root/scripts/testdata/e2e-redaction"
redaction_dst="$STOAT_HOME/recipes/redaction"
if [ -e "$redaction_dst" ] || [ -L "$redaction_dst" ]; then
	fail "refusing to overwrite existing recipe: $redaction_dst"
fi
mkdir -p "$redaction_dst"
cp "$redaction_src/recipe.toml" "$redaction_src/install.sh" "$redaction_dst/"
chmod 755 "$redaction_dst/install.sh"

say "assert: docker recipe contract"
"$STOAT" update "$VM" --recipes xfce,docker,redaction --set docker.user=dev --secret redaction.token
"$STOAT" apply "$VM"
"$STOAT" wait "$VM" --healthy --timeout 90s

status=$(
	"$STOAT" get "$VM" --json
)
printf '%s\n' "$status" | grep -q '"socket":"/var/run/docker.sock"' \
	|| fail "docker did not report its socket output"
printf '%s\n' "$status" | grep -q '"socket":"/var/run/redaction.sock"' \
	|| fail "redaction fixture did not report its output"
printf '%s\n' "$status" | grep -q '"health":"ok"' \
	|| fail "healthy wait did not record an ok status"
printf '%s\n' "$status" | grep -q '"token":"<set>"' \
	|| fail "redaction recipe secret was not represented as <set>"
if printf '%s\n' "$status" | grep -Fq "$E2E_SECRET"; then
	fail "secret sentinel reached stoat get output"
fi

say "assert: non-secret param reruns"
"$STOAT" update "$VM" --set docker.user=e2e-rerun
"$STOAT" apply "$VM" --dry-run | grep -q 'params changed' \
	|| fail "docker param change did not trigger a params-changed rerun"
