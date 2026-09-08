package gce

import (
	"time"

	computepb "cloud.google.com/go/compute/apiv1/computepb"
)

// WarnWithin is how far ahead of either deadline the CLI starts warning.
const WarnWithin = time.Hour

// hardDeadline is lastStartTimestamp plus scheduling.maxRunDuration. Compute
// v1's instances.get response carries no scheduling.terminationTimestamp
// field (docket d42): a probe against a real instance confirmed the field is
// simply absent, so a warning that read it would never fire.
func hardDeadline(inst *computepb.Instance) (time.Time, bool) {
	dur := inst.GetScheduling().GetMaxRunDuration()
	if dur == nil || dur.GetSeconds() == 0 {
		return time.Time{}, false
	}
	start, err := time.Parse(time.RFC3339, inst.GetLastStartTimestamp())
	if err != nil {
		return time.Time{}, false
	}
	return start.Add(time.Duration(dur.GetSeconds()) * time.Second).UTC(), true
}

// nearest picks whichever of the hard and soft deadlines comes first. A zero
// time.Time means that deadline does not apply.
func nearest(hard, soft, now time.Time) (when time.Time, which string, ok bool) {
	switch {
	case hard.IsZero() && soft.IsZero():
		return time.Time{}, "", false
	case hard.IsZero():
		return soft, "soft deadline", true
	case soft.IsZero():
		return hard, "run-time limit", true
	case soft.Before(hard):
		return soft, "soft deadline", true
	default:
		return hard, "run-time limit", true
	}
}
