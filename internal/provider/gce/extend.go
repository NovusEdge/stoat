package gce

import (
	"context"
	"fmt"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/settings"
)

// Extend moves v's soft deadline forward by d from now, using
// instances.setLabels. It never touches scheduling: instances.setScheduling
// needs a stopped instance, so maxRunDuration cannot move while v runs
// (docket d42). A new deadline past the hard one is refused rather than
// silently capped: it is a promise this provider cannot keep without a stop
// and start, and the caller should decide whether to do that.
func (Provider) Extend(ctx context.Context, v *config.VM, d time.Duration) (time.Time, error) {
	s, err := vmSettings(v)
	if err != nil {
		return time.Time{}, err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = c.Close() }()
	return extend(ctx, c, s, v, d, time.Now())
}

func extend(ctx context.Context, c *compute.InstancesClient, s settings.GCE, v *config.VM, d time.Duration, now time.Time) (time.Time, error) {
	inst, err := c.Get(ctx, &computepb.GetInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return time.Time{}, fmt.Errorf("gce: getting %s: %w", v.Name, err)
	}
	newDeadline := now.Add(d)
	if hard, ok := hardDeadline(inst); ok && newDeadline.After(hard) {
		return time.Time{}, fmt.Errorf("gce: %s's run-time limit is %s; extending past it needs a stop and start to reset that limit first",
			v.Name, hard.UTC().Format(time.RFC3339))
	}

	op, err := c.SetLabels(ctx, &computepb.SetLabelsInstanceRequest{
		Project:  s.Project,
		Zone:     s.Zone,
		Instance: v.Name,
		InstancesSetLabelsRequestResource: &computepb.InstancesSetLabelsRequest{
			Labels:           withSoftDeadline(inst.GetLabels(), newDeadline),
			LabelFingerprint: inst.LabelFingerprint,
		},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("gce: setting labels on %s: %w", v.Name, err)
	}
	if err := op.Wait(ctx); err != nil {
		return time.Time{}, fmt.Errorf("gce: setting labels on %s: %w", v.Name, err)
	}
	return newDeadline, nil
}
