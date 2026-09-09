package gce

import (
	"strings"
	"testing"
)

func TestFirewallRuleIsScopedToOneVMAndOneSource(t *testing.T) {
	r := firewallRule("cloudy", "203.0.113.1/32")
	if len(r.SourceRanges) != 1 || r.SourceRanges[0] != "203.0.113.1/32" {
		t.Errorf("source ranges = %v, want exactly the operator's address", r.SourceRanges)
	}
	if len(r.TargetTags) != 1 || !strings.Contains(r.TargetTags[0], "cloudy") {
		t.Errorf("target tags = %v, want this VM only", r.TargetTags)
	}
}
