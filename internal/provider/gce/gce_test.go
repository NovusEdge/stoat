package gce

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	compute "cloud.google.com/go/compute/apiv1"
	"google.golang.org/api/option"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// roundTripFunc lets a test supply instances.get's response body without a
// real client or network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeInstancesClient(t *testing.T, body string) *compute.InstancesClient {
	t.Helper()
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})
	c, err := compute.NewInstancesRESTClient(context.Background(),
		option.WithHTTPClient(&http.Client{Transport: rt}),
		option.WithoutAuthentication(),
		option.WithEndpoint("https://compute.googleapis.com/compute/v1/"))
	if err != nil {
		t.Fatalf("building a fake instances client: %v", err)
	}
	return c
}

func TestStatusMapsRunningStates(t *testing.T) {
	for raw, want := range map[string]bool{
		"PROVISIONING": true, "STAGING": true, "RUNNING": true,
		"STOPPING": false, "SUSPENDING": false, "SUSPENDED": false,
		"TERMINATED": false, "REPAIRING": false,
	} {
		if got := running[raw]; got != want {
			t.Errorf("running[%q] = %v, want %v", raw, got, want)
		}
	}
}

func TestStatusForReadsRawAndMapping(t *testing.T) {
	c := fakeInstancesClient(t, `{"status":"STOPPING"}`)
	defer c.Close()
	st, err := statusFor(context.Background(), c, settings.GCE{Project: "p", Zone: "z"}, &config.VM{Name: "cloudy"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Running {
		t.Error("Status().Running = true for STOPPING")
	}
	if st.Raw != "STOPPING" {
		t.Errorf("Status().Raw = %q, want STOPPING", st.Raw)
	}
}

func TestEndpointReadsTheExternalAddress(t *testing.T) {
	c := fakeInstancesClient(t, `{"networkInterfaces":[{"accessConfigs":[{"natIP":"34.12.221.212"}]}]}`)
	defer c.Close()
	v := &config.VM{Name: "cloudy", Dir: "/data/vms/cloudy"}
	ep, err := endpointFor(context.Background(), c, settings.GCE{Project: "p", Zone: "z"}, v)
	if err != nil {
		t.Fatal(err)
	}
	if ep.Host != "34.12.221.212" {
		t.Errorf("Endpoint().Host = %q, want the natIP", ep.Host)
	}
	if ep.Port != 22 {
		t.Errorf("Endpoint().Port = %d, want 22", ep.Port)
	}
	if ep.KnownHosts == "" {
		t.Error("Endpoint().KnownHosts is empty; a routable host must pin its host key")
	}
}

func TestCapabilitiesDeclareTheUnsupportedSet(t *testing.T) {
	caps := Provider{}.Capabilities(&config.VM{})
	byName := make(map[string]string, len(caps))
	for _, c := range caps {
		byName[c.Name] = c.Status
	}
	for _, name := range []string{"share", "screenshot", "sendkey", "console_log", "forward", "snapshot", "clone", "update.ram", "update.cpu"} {
		if byName[name] != "unsupported" {
			t.Errorf("capability %q = %q, want unsupported", name, byName[name])
		}
	}
}
