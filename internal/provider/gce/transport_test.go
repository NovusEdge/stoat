package gce

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// sequencedTransport returns each body in order on successive requests and
// repeats the last body once the sequence is exhausted, so a long-running
// operation's second Poll (after Wait's Done() check on the first) still
// gets an answer.
func sequencedTransport(t *testing.T, bodies ...string) http.RoundTripper {
	t.Helper()
	raw := make([]string, len(bodies))
	for i, name := range bodies {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatalf("reading testdata/%s: %v", name, err)
		}
		raw[i] = string(b)
	}
	n := 0
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		i := n
		if i >= len(raw) {
			i = len(raw) - 1
		}
		n++
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(raw[i])),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})
}

func fakeCompute(t *testing.T, bodies ...string) (*compute.InstancesClient, *compute.FirewallsClient) {
	t.Helper()
	rt := sequencedTransport(t, bodies...)
	opts := []option.ClientOption{
		option.WithHTTPClient(&http.Client{Transport: rt}),
		option.WithoutAuthentication(),
		option.WithEndpoint("https://compute.googleapis.com/compute/v1/"),
	}
	ic, err := compute.NewInstancesRESTClient(context.Background(), opts...)
	if err != nil {
		t.Fatalf("building fake instances client: %v", err)
	}
	fc, err := compute.NewFirewallsRESTClient(context.Background(), opts...)
	if err != nil {
		t.Fatalf("building fake firewalls client: %v", err)
	}
	return ic, fc
}

// TestTransportCreatesFirewallThenInstance replays what Create does: an
// Insert on the Firewalls API, waited to completion, then an Insert on the
// Instances API built from insertRequest, also waited to completion. Each
// Insert's initial response is RUNNING; Wait's first Poll finds it DONE, the
// shape a real create actually returns rather than a synchronous success.
func TestTransportCreatesFirewallThenInstance(t *testing.T) {
	ic, fc := fakeCompute(t, "operation_running.json", "operation_done.json")
	defer func() { _ = ic.Close() }()
	defer func() { _ = fc.Close() }()
	ctx := context.Background()

	fwOp, err := fc.Insert(ctx, &computepb.InsertFirewallRequest{
		Project:          "p",
		FirewallResource: firewallRule("cloudy", "203.0.113.1/32"),
	})
	if err != nil {
		t.Fatalf("Firewalls.Insert: %v", err)
	}
	if err := fwOp.Wait(ctx); err != nil {
		t.Fatalf("firewall op.Wait: %v", err)
	}

	req, err := insertRequest(minimalVM(), minimalSettings(), "img", "#cloud-config\n", "203.0.113.1/32")
	if err != nil {
		t.Fatal(err)
	}
	insOp, err := ic.Insert(ctx, req)
	if err != nil {
		t.Fatalf("Instances.Insert: %v", err)
	}
	if err := insOp.Wait(ctx); err != nil {
		t.Fatalf("instance op.Wait: %v", err)
	}
}

func TestTransportStartsAnInstance(t *testing.T) {
	ic, _ := fakeCompute(t, "operation_running.json", "operation_done.json")
	defer func() { _ = ic.Close() }()
	ctx := context.Background()
	op, err := ic.Start(ctx, &computepb.StartInstanceRequest{Project: "p", Zone: "europe-west4-a", Instance: "cloudy"})
	if err != nil {
		t.Fatalf("Instances.Start: %v", err)
	}
	if err := op.Wait(ctx); err != nil {
		t.Fatalf("op.Wait: %v", err)
	}
}

func TestTransportStopsAnInstance(t *testing.T) {
	ic, _ := fakeCompute(t, "operation_running.json", "operation_done.json")
	defer func() { _ = ic.Close() }()
	ctx := context.Background()
	op, err := ic.Stop(ctx, &computepb.StopInstanceRequest{Project: "p", Zone: "europe-west4-a", Instance: "cloudy"})
	if err != nil {
		t.Fatalf("Instances.Stop: %v", err)
	}
	if err := op.Wait(ctx); err != nil {
		t.Fatalf("op.Wait: %v", err)
	}
}

// TestTransportDestroysInstanceThenFirewall replays Destroy's order: the
// instance is gone before its firewall rule is deleted.
func TestTransportDestroysInstanceThenFirewall(t *testing.T) {
	ic, fc := fakeCompute(t, "operation_running.json", "operation_done.json")
	defer func() { _ = ic.Close() }()
	defer func() { _ = fc.Close() }()
	ctx := context.Background()

	delOp, err := ic.Delete(ctx, &computepb.DeleteInstanceRequest{Project: "p", Zone: "europe-west4-a", Instance: "cloudy"})
	if err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}
	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("delete op.Wait: %v", err)
	}

	if err := deleteFirewallRule(ctx, fc, settings.GCE{Project: "p"}, "cloudy"); err != nil {
		t.Fatalf("deleteFirewallRule: %v", err)
	}
}

// TestTransportStatusReadsARecordedInstance exercises Status against a full
// instances.get response recorded from a real project. Its scheduling
// object carries no terminationTimestamp field: the compute v1 API never
// returns one (docket d42), so hardDeadline must work from lastStartTimestamp
// and maxRunDuration alone.
func TestTransportStatusReadsARecordedInstance(t *testing.T) {
	ic, _ := fakeCompute(t, "instance_get.json")
	defer func() { _ = ic.Close() }()
	ctx := context.Background()
	v := &config.VM{Name: "cloudy", Dir: "/data/vms/cloudy"}
	s := settings.GCE{Project: "engrammic", Zone: "europe-west4-a"}

	st, err := statusFor(ctx, ic, s, v)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Raw != "RUNNING" {
		t.Errorf("statusFor() = %+v, want a running RUNNING status", st)
	}

	ep, err := endpointFor(ctx, ic, s, v)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Host != "34.12.221.212" {
		t.Errorf("endpointFor().Host = %q, want the recorded natIP", ep.Host)
	}

	inst, err := ic.Get(ctx, &computepb.GetInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		t.Fatal(err)
	}
	if inst.GetScheduling().GetMaxRunDuration().GetSeconds() != 28800 {
		t.Errorf("maxRunDuration = %d, want 28800", inst.GetScheduling().GetMaxRunDuration().GetSeconds())
	}
	deadline, ok := hardDeadline(inst)
	if !ok {
		t.Fatal("hardDeadline() not ok against a recorded running instance")
	}
	if deadline.IsZero() {
		t.Error("hardDeadline() returned the zero time")
	}
}
