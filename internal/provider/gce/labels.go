package gce

import (
	"strings"
	"time"
)

// GCE label values allow only lowercase letters, digits, underscore and
// hyphen: no colon, plus or uppercase, so an RFC3339 timestamp cannot pass
// through unmodified.
const (
	labelOwned        = "stoat-owned"
	labelVM           = "stoat-vm"
	labelSoftDeadline = "stoat-soft-deadline"
)

// withSoftDeadline sets the operator-requested deadline label on labels,
// copying it first so the caller's map is untouched.
func withSoftDeadline(labels map[string]string, t time.Time) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out[labelSoftDeadline] = encodeTimestamp(t)
	return out
}

// softDeadline reads the label withSoftDeadline writes.
func softDeadline(labels map[string]string) (time.Time, bool) {
	v, ok := labels[labelSoftDeadline]
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, decodeTimestamp(v))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func encodeTimestamp(t time.Time) string {
	s := t.UTC().Format(time.RFC3339)
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ":", "-")
	return strings.ReplaceAll(s, "+", "-")
}

// decodeTimestamp reverses encodeTimestamp. RFC3339's only "-" that is not a
// separator stand-in is in the date and the zone sign, both fixed positions
// this label always carries as "z" (UTC), so blind hyphen-to-colon
// replacement on the time portion is safe.
func decodeTimestamp(s string) string {
	i := strings.IndexByte(s, 't')
	if i < 0 {
		return s
	}
	rest := strings.ReplaceAll(s[i+1:], "-", ":")
	rest = strings.ReplaceAll(rest, "z", "Z")
	return s[:i] + "T" + rest
}

func ownershipLabels(vm string) map[string]string {
	return map[string]string{
		labelOwned: "true",
		labelVM:    vm,
	}
}
