package gce

import (
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"
)

// firewallName is the rule's own resource name, distinct from vmTag so a
// firewall lookup and an instance tag lookup can never collide.
func firewallName(vm string) string { return "stoat-ssh-" + vm }

// firewallRule allows tcp:22 from sourceRange to the one instance tagged
// vmTag(vm). Each VM gets its own rule so destroying one VM's rule can never
// affect another's ingress.
func firewallRule(vm, sourceRange string) *computepb.Firewall {
	return &computepb.Firewall{
		Name:         proto.String(firewallName(vm)),
		SourceRanges: []string{sourceRange},
		TargetTags:   []string{vmTag(vm)},
		Allowed: []*computepb.Allowed{{
			IPProtocol: proto.String("tcp"),
			Ports:      []string{"22"},
		}},
		Direction: proto.String("INGRESS"),
	}
}
