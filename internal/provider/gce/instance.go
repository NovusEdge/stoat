package gce

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/protobuf/proto"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// maxMetadataValueBytes is compute's own cap on one metadata value (docket
// d24). Rejecting an oversize seed here means the failure names the seed,
// not an opaque 400 from the API.
const maxMetadataValueBytes = 256 * 1024

// defaultMaxRunDuration bounds every instance this provider creates. Paired
// with instanceTerminationAction=STOP, a VM nobody stops manually still
// stops billing on its own.
const defaultMaxRunDuration = 6 * time.Hour

// operatorEndpoint echoes back the caller's address in a bare-text body.
// domains.google.com/checkip filled this role until Google Domains was
// retired; it now 301s to an HTML page. ipify has no IPv6 records on this
// hostname, so the response is always v4, matching the instance's v4-only
// ONE_TO_ONE_NAT access config (api64.ipify.org would resolve v6 first on a
// dual-stack host and produce a firewall rule the instance can never match).
const operatorEndpoint = "https://api.ipify.org"

// insertRequest builds the instances.insert request for v. It takes no
// client and makes no call, so every shape it can produce is covered by a
// table test with no network.
func insertRequest(v *config.VM, s settings.GCE, image, seed, sourceRange string) (*computepb.InsertInstanceRequest, error) {
	if len(seed) > maxMetadataValueBytes {
		return nil, fmt.Errorf("seed is %d bytes, over metadata's %d byte limit", len(seed), maxMetadataValueBytes)
	}
	diskGB, err := diskSizeGB(v.Disk)
	if err != nil {
		return nil, fmt.Errorf("disk size %q: %w", v.Disk, err)
	}

	tag := vmTag(v.Name)
	inst := &computepb.Instance{
		Name:        proto.String(v.Name),
		MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", s.Zone, machineType(v))),
		Labels:      ownershipLabels(v.Name),
		Tags:        &computepb.Tags{Items: []string{tag}},
		Disks: []*computepb.AttachedDisk{{
			Boot:       proto.Bool(true),
			AutoDelete: proto.Bool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: proto.String(image),
				DiskSizeGb:  proto.Int64(diskGB),
			},
		}},
		Metadata: &computepb.Metadata{
			Items: []*computepb.Items{{Key: proto.String("user-data"), Value: proto.String(seed)}},
		},
		NetworkInterfaces: []*computepb.NetworkInterface{{
			AccessConfigs: []*computepb.AccessConfig{{
				Name: proto.String("External NAT"),
				Type: proto.String("ONE_TO_ONE_NAT"),
			}},
		}},
		Scheduling: &computepb.Scheduling{
			MaxRunDuration:            &computepb.Duration{Seconds: proto.Int64(int64(defaultMaxRunDuration.Seconds()))},
			InstanceTerminationAction: proto.String("STOP"),
		},
	}
	return &computepb.InsertInstanceRequest{
		Project:          s.Project,
		Zone:             s.Zone,
		InstanceResource: inst,
	}, nil
}

// vmTag is the network tag one VM's firewall rule and instance share, so the
// rule reaches exactly this instance and no other.
func vmTag(vm string) string { return "stoat-" + vm }

// machineType picks a custom e2 shape sized to v. e2-custom requires memory
// as a multiple of 256 MB; RAM values from the form are already MB-aligned
// by core's validation, but round up here rather than trust that.
func machineType(v *config.VM) string {
	cpus := v.CPUs
	if cpus <= 0 {
		cpus = 1
	}
	ram := v.RAM
	if ram <= 0 {
		ram = 1024
	}
	if rem := ram % 256; rem != 0 {
		ram += 256 - rem
	}
	return fmt.Sprintf("e2-custom-%d-%d", cpus, ram)
}

// diskSizeGB reads a qemu-img-style size ("20G", "8T") as whole gigabytes,
// the unit computepb.AttachedDiskInitializeParams.DiskSizeGb takes. Anything
// smaller than a gigabyte rounds up to compute's own 10 GB boot disk minimum.
func diskSizeGB(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'M':
		s = s[:len(s)-1]
	case 'G':
		s = s[:len(s)-1]
	case 'T':
		mult, s = 1024, s[:len(s)-1]
	default:
		return 0, fmt.Errorf("use a size like 20G")
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("use a size like 20G")
	}
	gb := int64(n) * mult
	if gb < 10 {
		gb = 10
	}
	return gb, nil
}

// sourceRangeFor is the CIDR the SSH firewall rule admits. A configured
// source_range wins outright and skips the lookup: an operator whose SSH
// traffic leaves by a different path than an HTTPS request gets back an
// address the instance never sees, and retrying does not fix that.
func sourceRangeFor(ctx context.Context, s settings.GCE) (string, error) {
	r := strings.TrimSpace(s.SourceRange)
	if r == "" {
		return operatorRange(ctx)
	}
	if _, _, err := net.ParseCIDR(r); err != nil {
		return "", fmt.Errorf("providers.gce source_range %q: %w", r, err)
	}
	return r, nil
}

// operatorRange asks operatorEndpoint what address it saw the request come
// from, and returns it as a single-address CIDR the firewall rule can use
// as its source range. The instance always gets a v4-only ONE_TO_ONE_NAT
// access config, so a v6 answer here would produce a rule the instance can
// never match; operatorRange rejects one rather than open a mismatched rule.
func operatorRange(ctx context.Context) (string, error) {
	return operatorRangeWith(ctx, http.DefaultClient)
}

func operatorRangeWith(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, operatorEndpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("looking up the operator's address: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(body))
	ip := net.ParseIP(addr)
	if ip == nil {
		return "", fmt.Errorf("operator address lookup returned %q, not an IP", addr)
	}
	if ip.To4() == nil {
		return "", fmt.Errorf("operator address lookup returned %q, an IPv6 address; the instance is v4-only", addr)
	}
	return rangeFor(addr), nil
}

// rangeFor scopes addr to itself: /32 for IPv4, /128 for IPv6 (docket d45).
// A /32 on an IPv6 address would open most of the operator's assigned
// prefix, not just their one address.
func rangeFor(addr string) string {
	if strings.Contains(addr, ":") {
		return addr + "/128"
	}
	return addr + "/32"
}
