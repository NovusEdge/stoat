package gce

import (
	"strings"
	"testing"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"
)

func instanceWithDeadlines(t *testing.T, sinceStart, softIn time.Duration) *computepb.Instance {
	t.Helper()
	start := time.Now().Add(-sinceStart)
	return &computepb.Instance{
		Name:               proto.String("cloudy"),
		Status:             proto.String("RUNNING"),
		LastStartTimestamp: proto.String(start.Format(time.RFC3339)),
		Scheduling: &computepb.Scheduling{
			MaxRunDuration: &computepb.Duration{Seconds: proto.Int64(int64((sinceStart + time.Hour).Seconds()))},
		},
		Labels: withSoftDeadline(ownershipLabels("cloudy"), time.Now().Add(softIn)),
	}
}

func TestListFiltersOnTheOwnershipLabel(t *testing.T) {
	req := listRequest("engrammic")
	if !strings.Contains(req.GetFilter(), "labels.stoat-owned") {
		t.Errorf("filter = %q, want the ownership label", req.GetFilter())
	}
	if req.GetProject() != "engrammic" {
		t.Errorf("project = %q, want engrammic", req.GetProject())
	}
}

func TestRemoteFromInstanceCarriesBothDeadlines(t *testing.T) {
	inst := instanceWithDeadlines(t, 3*time.Hour, 30*time.Minute)
	got := remoteFrom(inst)
	if got.HardDeadline.IsZero() || got.SoftDeadline.IsZero() {
		t.Errorf("Remote = %+v, want both deadlines", got)
	}
	if got.VM != "cloudy" {
		t.Errorf("VM = %q, want cloudy", got.VM)
	}
}

func TestRemoteReportsAnUnnamedInstance(t *testing.T) {
	inst := &computepb.Instance{
		Name:   proto.String("stoat-orphan"),
		Status: proto.String("RUNNING"),
		Labels: map[string]string{"stoat-owned": "true"},
	}
	if got := remoteFrom(inst); got.VM != "" {
		t.Errorf("VM = %q, want empty so the report shows it as unnamed", got.VM)
	}
}

func TestRemoteFromReportsStoppedSinceOnlyWhenTerminated(t *testing.T) {
	inst := &computepb.Instance{
		Name:              proto.String("cloudy"),
		Status:            proto.String("TERMINATED"),
		LastStopTimestamp: proto.String(time.Now().Add(-6 * 24 * time.Hour).Format(time.RFC3339)),
		Labels:            ownershipLabels("cloudy"),
	}
	got := remoteFrom(inst)
	if got.StoppedSince < 5*24*time.Hour {
		t.Errorf("StoppedSince = %s, want roughly 6 days", got.StoppedSince)
	}

	running := &computepb.Instance{
		Name:   proto.String("cloudy"),
		Status: proto.String("RUNNING"),
		Labels: ownershipLabels("cloudy"),
	}
	if got := remoteFrom(running); got.StoppedSince != 0 {
		t.Errorf("StoppedSince = %s for a running instance, want zero", got.StoppedSince)
	}
}
