#!/usr/bin/env bash
# End-to-end gate for the gce provider against a real GCP project.
#
# Every defect this gate exists to catch was invisible to the unit suite:
# create demanding a local image gce never opens, create inserting a running
# instance, sshx dialling loopback for a cloud guest. None of them can be
# reproduced without a real API, which is why this is a separate gate rather
# than a test.
#
# Skips unless STOAT_GCE_LIVE_PROJECT and STOAT_GCE_LIVE_ZONE are set.
#
#   STOAT_GCE_LIVE_PROJECT=my-project STOAT_GCE_LIVE_ZONE=europe-west4-a \
#     bash tests/gce-live-gate.sh
set -euo pipefail

if [ -z "${STOAT_GCE_LIVE_PROJECT:-}" ] || [ -z "${STOAT_GCE_LIVE_ZONE:-}" ]; then
  echo "skip: set STOAT_GCE_LIVE_PROJECT and STOAT_GCE_LIVE_ZONE to run the gce live gate"
  exit 0
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
vm="livegate-$$"
project="$STOAT_GCE_LIVE_PROJECT"
zone="$STOAT_GCE_LIVE_ZONE"

# Runs on every exit, including a failure or a kill. The instance also
# carries its own short run-time limit, so a gate killed hard enough to skip
# this trap still stops billing on its own.
cleanup() {
  "$work/stoat" rm -y "$vm" >/dev/null 2>&1 || true
  gcloud compute instances delete "$vm" --project="$project" --zone="$zone" --quiet >/dev/null 2>&1 || true
  gcloud compute firewall-rules delete "stoat-ssh-$vm" --project="$project" --quiet >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

(cd "$repo" && go build -o "$work/stoat" ./cmd/stoat)

export STOAT_HOME="$work/home"
mkdir -p "$STOAT_HOME"
cat >"$STOAT_HOME/config.toml" <<EOF
[providers.gce]
project = "$project"
zone = "$zone"
max_run_duration = "30m"
EOF

echo "== create =="
"$work/stoat" create "$vm" --provider gce --image ubuntu-24.04 --ram 4096 --cpus 2 >/dev/null

# create writes a record and nothing else. An instance existing here means
# the insert leaked back into Create, which is what made up refuse with
# "already running".
if gcloud compute instances describe "$vm" --project="$project" --zone="$zone" >/dev/null 2>&1; then
  fail "create made an instance; it must only write the record"
fi
"$work/stoat" ls | grep -q "$vm" || fail "ls does not show the created VM"
"$work/stoat" ls | grep -q "gce" || fail "ls does not show gce in the WHERE column"

echo "== up =="
"$work/stoat" up "$vm" >/dev/null
"$work/stoat" wait "$vm" --until reachable --timeout 5m >/dev/null || fail "VM never became reachable"

echo "== guest =="
who="$("$work/stoat" exec "$vm" -- whoami)"
[ "$who" = "stoat" ] || fail "expected to connect as stoat, got '$who'"
"$work/stoat" exec "$vm" -- sh -c 'sudo -n true' || fail "no passwordless sudo for the seeded account"
ci="$("$work/stoat" exec "$vm" -- sh -c 'cloud-init status')"
echo "$ci" | grep -q done || fail "cloud-init did not finish: $ci"

echo "== copy =="
echo "gate-marker" >"$work/marker"
"$work/stoat" cp "$work/marker" "$vm:/home/stoat/marker" >/dev/null
got="$("$work/stoat" exec "$vm" -- cat /home/stoat/marker)"
[ "$got" = "gate-marker" ] || fail "copied file read back as '$got'"

echo "== capability refusals =="
for op in "snapshot $vm tag1" "forward $vm 8080:80"; do
  # shellcheck disable=SC2086
  out="$("$work/stoat" --json $op 2>&1 || true)"
  echo "$out" | grep -q capability_unavailable || fail "$op did not report capability_unavailable: $out"
  echo "$out" | grep -q provider_unsupported || fail "$op did not carry a reason: $out"
done

echo "== prune stays quiet about a healthy VM =="
# Running, with a local record, matches none of prune's three classes.
out="$("$work/stoat" prune 2>&1 || true)"
echo "$out" | grep -q "$vm" && fail "prune reported a healthy running VM: $out"

echo "== extend =="
# Under the 30m run-time limit this gate sets. Extending past the hard
# deadline is refused by design, which the next check exercises.
"$work/stoat" gce extend "$vm" 10m >/dev/null || fail "gce extend failed"
if "$work/stoat" gce extend "$vm" 400h >/dev/null 2>&1; then
  fail "extend accepted a deadline past the run-time limit"
fi
state="$(gcloud compute instances describe "$vm" --project="$project" --zone="$zone" --format='value(status)')"
[ "$state" = "RUNNING" ] || fail "extend left the instance $state; it must keep running"
gcloud compute instances describe "$vm" --project="$project" --zone="$zone" \
  --format='value(labels.stoat-soft-deadline)' | grep -q . \
  || fail "extend did not write a soft-deadline label"

echo "== persistence across down and up =="
"$work/stoat" down "$vm" >/dev/null
gcloud compute disks describe "$vm" --project="$project" --zone="$zone" --format='value(status)' | grep -q READY \
  || fail "boot disk did not survive the stop"

# A stopped instance keeps billing for that disk, so prune names it. This is
# the one class the gate can produce without faking a lost record.
out="$("$work/stoat" prune 2>&1 || true)"
echo "$out" | grep -q "$vm" || fail "prune does not report the stopped instance: $out"
gcloud compute instances describe "$vm" --project="$project" --zone="$zone" >/dev/null 2>&1 \
  || fail "prune deleted the instance; a sweep must never remove a remote resource"

"$work/stoat" up "$vm" >/dev/null
"$work/stoat" wait "$vm" --until reachable --timeout 5m >/dev/null || fail "VM unreachable after restart"
got="$("$work/stoat" exec "$vm" -- cat /home/stoat/marker)"
[ "$got" = "gate-marker" ] || fail "home directory did not survive the restart"

echo "== teardown =="
"$work/stoat" down "$vm" >/dev/null
"$work/stoat" rm -y "$vm" >/dev/null

for kind in "instances describe $vm --zone=$zone" "disks describe $vm --zone=$zone"; do
  # shellcheck disable=SC2086
  if gcloud compute $kind --project="$project" >/dev/null 2>&1; then
    fail "rm left a $kind behind"
  fi
done
if gcloud compute firewall-rules describe "stoat-ssh-$vm" --project="$project" >/dev/null 2>&1; then
  fail "rm left the firewall rule behind"
fi

echo "ok: gce live gate passed against $project/$zone"
