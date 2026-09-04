#!/usr/bin/env sh
# End-to-end lifecycle check: one `stoat up` must take a fresh Alpine disk VM
# from nothing to a desktop with a working mouse, with zero manual steps.
#
# The chain under test: install -> auto-restart after poweroff -> auto-apply
# the xfce recipe -> reboot once so eudev/libinput take effect. Each stage is
# code that has regressed before, so this asserts the OUTCOME a user sees:
# udev is the device manager and Xorg drives input through libinput.
#
# Runs against the real data root by default, under a unique VM name it deletes
# on exit. Set STOAT_HOME to isolate it from your VMs. Needs KVM and network;
# the xfce apk pull is ~1.4GB, so budget ~15 minutes.
set -eu

IMAGE=alpine-standard
RECIPE=xfce
VM="e2e-$$"
UP_TIMEOUT=1200   # install + xfce pull + reboot-once
X_TIMEOUT=90      # Xorg restart after the reboot-once

# Prefer the just-built binary over whatever is on PATH.
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
STOAT="$root/stoat"
[ -x "$STOAT" ] || STOAT=stoat

say() { printf '\n=== %s ===\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }

cleanup() {
	"$STOAT" down "$VM" >/dev/null 2>&1 || true
	"$STOAT" rm "$VM" -y >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

say "build"
( cd "$root" && go build -o stoat ./cmd/stoat )

say "image $IMAGE"
"$STOAT" images 2>/dev/null | grep -q "^$IMAGE .*downloaded" || "$STOAT" pull "$IMAGE"

say "create $VM (alpine disk, recipe $RECIPE)"
"$STOAT" create "$VM" --image "$IMAGE" --mode disk --recipes "$RECIPE"

say "up $VM (install -> restart -> apply -> reboot-once)"
timeout "$UP_TIMEOUT" "$STOAT" up "$VM" || fail "up did not finish in ${UP_TIMEOUT}s"

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
while [ "$end" -lt "$X_TIMEOUT" ]; do
	if "$STOAT" exec "$VM" -- sh -c \
		"grep -q \"Using input driver 'libinput'\" /var/log/Xorg.0.log 2>/dev/null"; then
		printf '\nPASS: %s reached a clickable xfce desktop with no manual steps\n' "$VM"
		exit 0
	fi
	end=$((end + 5))
	sleep 5
done
fail "no libinput device in Xorg.0.log after ${X_TIMEOUT}s"
