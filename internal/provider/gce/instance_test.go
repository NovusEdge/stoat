package gce

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

func fakeHTTPClient(body string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
		}, nil
	})}
}

func minimalVM() *config.VM {
	return &config.VM{Name: "cloudy", RAM: 2048, CPUs: 2, Disk: "20G"}
}

func minimalSettings() settings.GCE {
	return settings.GCE{Project: "p", Zone: "europe-west4-a"}
}

func TestInsertRequestCarriesTheSeedAsUserData(t *testing.T) {
	req, err := insertRequest(&config.VM{Name: "cloudy", RAM: 4096, CPUs: 2, Disk: "20G"},
		settings.GCE{Project: "p", Zone: "europe-west4-a"},
		"projects/ubuntu-os-cloud/global/images/family/ubuntu-2404-lts-amd64",
		"#cloud-config\nusers: []\n", "203.0.113.1/32")
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, item := range req.InstanceResource.Metadata.Items {
		if item.GetKey() == "user-data" {
			seen = true
			if !strings.HasPrefix(item.GetValue(), "#cloud-config") {
				t.Errorf("user-data = %q, want the seed verbatim", item.GetValue())
			}
		}
	}
	if !seen {
		t.Error("no user-data metadata item; the guest would boot unprovisioned")
	}
}

func TestInsertRequestAttachesNoServiceAccount(t *testing.T) {
	req, _ := insertRequest(minimalVM(), minimalSettings(), "img", "#cloud-config\n", "203.0.113.1/32")
	if len(req.InstanceResource.ServiceAccounts) != 0 {
		t.Error("instance carries a service account; guest code could reach the GCP API")
	}
}

func TestInsertRequestSetsARunTimeLimit(t *testing.T) {
	req, _ := insertRequest(minimalVM(), minimalSettings(), "img", "#cloud-config\n", "203.0.113.1/32")
	s := req.InstanceResource.Scheduling
	if s.GetMaxRunDuration().GetSeconds() == 0 {
		t.Error("no maxRunDuration; an orphaned instance would bill forever")
	}
	if s.GetInstanceTerminationAction() != "STOP" {
		t.Errorf("termination action = %q, want STOP", s.GetInstanceTerminationAction())
	}
}

func TestInsertRequestCarriesOwnershipLabels(t *testing.T) {
	req, _ := insertRequest(minimalVM(), minimalSettings(), "img", "#cloud-config\n", "203.0.113.1/32")
	if req.InstanceResource.Labels["stoat-owned"] != "true" {
		t.Error("no stoat-owned label; prune could not find this instance")
	}
	if req.InstanceResource.Labels["stoat-vm"] != "cloudy" {
		t.Errorf("stoat-vm label = %q, want cloudy", req.InstanceResource.Labels["stoat-vm"])
	}
}

func TestInsertRequestRejectsAnOversizeSeed(t *testing.T) {
	big := strings.Repeat("x", 257*1024)
	if _, err := insertRequest(minimalVM(), minimalSettings(), "img", big, "203.0.113.1/32"); err == nil {
		t.Error("insertRequest() = nil error for a seed over the 256 KB metadata limit")
	}
}

func TestOperatorRangeUsesTheRightPrefixLength(t *testing.T) {
	if got := rangeFor("89.166.32.165"); got != "89.166.32.165/32" {
		t.Errorf("rangeFor(v4) = %q", got)
	}
	if got := rangeFor("2001:14ba:788e:b400::19a"); got != "2001:14ba:788e:b400::19a/128" {
		t.Errorf("rangeFor(v6) = %q; a /32 on an IPv6 address opens a vast range", got)
	}
}

func TestOperatorRangeParsesABareV4Address(t *testing.T) {
	got, err := operatorRangeWith(context.Background(), fakeHTTPClient("89.166.32.165"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "89.166.32.165/32" {
		t.Errorf("operatorRangeWith() = %q, want 89.166.32.165/32", got)
	}
}

// domains.google.com/checkip now 301s to an HTML page instead of answering
// with a bare address; this is that failure mode reproduced with a fake
// transport instead of a live request.
func TestOperatorRangeRejectsANonIPBody(t *testing.T) {
	_, err := operatorRangeWith(context.Background(), fakeHTTPClient("<!DOCTYPE html><html>...</html>"))
	if err == nil {
		t.Error("operatorRangeWith() = nil error for an HTML body")
	}
}

func TestOperatorRangeRejectsIPv6(t *testing.T) {
	_, err := operatorRangeWith(context.Background(), fakeHTTPClient("2001:14ba:788e:b400::19a"))
	if err == nil {
		t.Error("operatorRangeWith() = nil error for an IPv6 address; the instance is v4-only")
	}
}

func TestSourceRangeForPrefersTheConfiguredValue(t *testing.T) {
	got, err := sourceRangeFor(context.Background(), settings.GCE{SourceRange: "203.0.113.0/24"})
	if err != nil {
		t.Fatalf("sourceRangeFor() error = %v", err)
	}
	if got != "203.0.113.0/24" {
		t.Errorf("sourceRangeFor() = %q, want the configured range with no lookup", got)
	}
}

func TestSourceRangeForRejectsAMalformedValue(t *testing.T) {
	_, err := sourceRangeFor(context.Background(), settings.GCE{SourceRange: "203.0.113.1"})
	if err == nil {
		t.Fatal("sourceRangeFor() = nil error for a bare address; a CIDR is required")
	}
	if !strings.Contains(err.Error(), "source_range") {
		t.Errorf("error %q must name the config key", err)
	}
}

func TestMaxRunDurationDefaultsToADay(t *testing.T) {
	got, err := maxRunDuration(settings.GCE{})
	if err != nil {
		t.Fatal(err)
	}
	if got != 24*time.Hour {
		t.Errorf("maxRunDuration() = %s, want 24h", got)
	}
}

func TestMaxRunDurationTakesTheConfiguredValue(t *testing.T) {
	got, err := maxRunDuration(settings.GCE{MaxRunDuration: "90m"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 90*time.Minute {
		t.Errorf("maxRunDuration() = %s, want 90m", got)
	}
}

func TestMaxRunDurationRejectsValuesComputeWouldReject(t *testing.T) {
	// 10s is under compute's 30s floor; 3000h is 125 days, past its 120 day
	// ceiling, where an instance schedule is the documented answer instead.
	for _, raw := range []string{"10s", "3000h", "banana"} {
		if _, err := maxRunDuration(settings.GCE{MaxRunDuration: raw}); err == nil {
			t.Errorf("maxRunDuration(%q) = nil error", raw)
		}
	}
}
