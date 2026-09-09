package gce

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"google.golang.org/api/option"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// countingTransport replays bodies in order (repeating the last once
// exhausted, matching sequencedTransport's contract for a polled Wait) and
// counts every request whose URL contains a substring in counts' keys.
type countingTransport struct {
	bodies []string
	counts map[string]*int
	n      int
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	for substr, count := range c.counts {
		if strings.Contains(r.URL.String(), substr) {
			*count++
		}
	}
	i := c.n
	if i >= len(c.bodies) {
		i = len(c.bodies) - 1
	}
	c.n++
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(c.bodies[i])),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// instanceJSON renders an instances.get body for an instance that started
// runFor ago, with a maxRunDuration of maxRun and the given labels.
func instanceJSON(t *testing.T, runFor, maxRun time.Duration, labels map[string]string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"name":               "cloudy",
		"status":             "RUNNING",
		"lastStartTimestamp": time.Now().Add(-runFor).UTC().Format(time.RFC3339),
		"labelFingerprint":   "abc123==",
		"labels":             labels,
		"scheduling": map[string]any{
			"maxRunDuration": map[string]any{"seconds": fmt.Sprintf("%d", int64(maxRun.Seconds()))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fakeClientWithCounts(t *testing.T, counts map[string]*int, bodies ...string) *compute.InstancesClient {
	t.Helper()
	rt := &countingTransport{bodies: bodies, counts: counts}
	c, err := compute.NewInstancesRESTClient(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: rt}),
		option.WithoutAuthentication(),
		option.WithEndpoint("https://compute.googleapis.com/compute/v1/"))
	if err != nil {
		t.Fatalf("building a fake instances client: %v", err)
	}
	return c
}

func TestExtendMovesTheSoftDeadlineOnly(t *testing.T) {
	stopCalls, setSchedulingCalls := 0, 0
	counts := map[string]*int{"/stop": &stopCalls, "setScheduling": &setSchedulingCalls}
	body := instanceJSON(t, 3*time.Hour, 8*time.Hour, map[string]string{"stoat-owned": "true"})
	c := fakeClientWithCounts(t, counts, body, `{"status":"RUNNING"}`, `{"status":"DONE"}`)
	defer func() { _ = c.Close() }()

	got, err := extend(context.Background(), c, settings.GCE{Project: "p", Zone: "z"}, &config.VM{Name: "cloudy"}, 90*time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stopCalls != 0 || setSchedulingCalls != 0 {
		t.Error("Extend touched Stop or SetScheduling; only the label may move")
	}
	if got.Before(time.Now().Add(80 * time.Minute)) {
		t.Errorf("new deadline = %s, want roughly 90m out", got)
	}
}

func TestExtendRefusesPastTheHardDeadline(t *testing.T) {
	body := instanceJSON(t, 1*time.Hour, 1*time.Hour, map[string]string{"stoat-owned": "true"})
	c := fakeClientWithCounts(t, nil, body)
	defer func() { _ = c.Close() }()

	_, err := extend(context.Background(), c, settings.GCE{Project: "p", Zone: "z"}, &config.VM{Name: "cloudy"}, 4*time.Hour, time.Now())
	if err == nil {
		t.Fatal("extend() = nil error past the hard deadline")
	}
	if !strings.Contains(err.Error(), "run-time limit") || !strings.Contains(err.Error(), "stop") {
		t.Errorf("error %q must name the hard deadline and how to reset it", err)
	}
}
