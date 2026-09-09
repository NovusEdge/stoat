package gce

import (
	"testing"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"
)

func TestHardDeadlineFromStartPlusDuration(t *testing.T) {
	inst := &computepb.Instance{
		LastStartTimestamp: proto.String("2026-09-08T13:00:00.000-07:00"),
		Scheduling:         &computepb.Scheduling{MaxRunDuration: &computepb.Duration{Seconds: proto.Int64(3600)}},
	}
	got, ok := hardDeadline(inst)
	if !ok {
		t.Fatal("hardDeadline() not ok for an instance carrying both fields")
	}
	want := time.Date(2026, 9, 8, 21, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("hardDeadline() = %s, want %s", got, want)
	}
}

func TestHardDeadlineAbsentWithoutADuration(t *testing.T) {
	inst := &computepb.Instance{LastStartTimestamp: proto.String("2026-09-08T13:00:00.000-07:00")}
	if _, ok := hardDeadline(inst); ok {
		t.Error("hardDeadline() ok for an instance with no run-time limit")
	}
}

func TestNearestPicksTheEarlierDeadline(t *testing.T) {
	now := time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)
	hard := now.Add(3 * time.Hour)
	soft := now.Add(30 * time.Minute)
	when, which, ok := nearest(hard, soft, now)
	if !ok || !when.Equal(soft) || which != "soft deadline" {
		t.Errorf("nearest() = %s %q %v, want the soft deadline", when, which, ok)
	}
}
