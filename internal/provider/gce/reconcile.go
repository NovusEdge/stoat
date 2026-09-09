package gce

import (
	"context"
	"fmt"
	"path"
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"

	"github.com/novusedge/stoat/internal/settings"
)

// Remote is one stoat-owned instance as GCP reports it.
type Remote struct {
	VM           string
	Zone         string
	Status       string
	StoppedSince time.Duration // zero while running
	SoftDeadline time.Time
	HardDeadline time.Time
}

// listRequest builds the aggregated-list request for project, filtered to
// the ownership label so one call covers every zone regardless of where an
// operator's config.toml currently points.
func listRequest(project string) *computepb.AggregatedListInstancesRequest {
	filter := fmt.Sprintf("labels.%s=true", labelOwned)
	return &computepb.AggregatedListInstancesRequest{
		Project: project,
		Filter:  &filter,
	}
}

// remoteFrom reads inst into a Remote. An instance carrying the ownership
// label but no stoat-vm label is one stoat created and failed to finish
// labelling; VM comes back empty rather than hiding the resource.
func remoteFrom(inst *computepb.Instance) Remote {
	hard, _ := hardDeadline(inst)
	soft, _ := softDeadline(inst.GetLabels())
	r := Remote{
		VM:           inst.GetLabels()[labelVM],
		Zone:         path.Base(inst.GetZone()),
		Status:       inst.GetStatus(),
		SoftDeadline: soft,
		HardDeadline: hard,
	}
	if r.Status == "TERMINATED" {
		if t, err := time.Parse(time.RFC3339, inst.GetLastStopTimestamp()); err == nil {
			r.StoppedSince = time.Since(t)
		}
	}
	return r
}

// List returns every stoat-owned instance in the configured project, across
// all zones, from one aggregated-list call. It reads config.toml itself,
// the same source vmSettings draws from, since a listing crosses zones and
// has no VM to read a project/zone pair off of.
func (Provider) List(ctx context.Context) ([]Remote, error) {
	cfg, err := settings.Load()
	if err != nil {
		return nil, err
	}
	s := cfg.Providers.GCE
	if s.Project == "" {
		return nil, fmt.Errorf("gce: providers.gce.project is not configured")
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()

	var out []Remote
	it := c.AggregatedList(ctx, listRequest(s.Project))
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("gce: listing instances: %w", err)
		}
		for _, inst := range pair.Value.GetInstances() {
			out = append(out, remoteFrom(inst))
		}
	}
	return out, nil
}
