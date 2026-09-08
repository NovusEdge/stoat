package gce

import (
	"testing"
	"time"
)

func TestSoftDeadlineRoundTripsThroughALabel(t *testing.T) {
	want := time.Date(2026, 9, 8, 21, 30, 0, 0, time.UTC)
	got, ok := softDeadline(withSoftDeadline(nil, want))
	if !ok || !got.Equal(want) {
		t.Errorf("softDeadline() = %s %v, want %s", got, ok, want)
	}
}

func TestLabelValueUsesOnlyPermittedCharacters(t *testing.T) {
	for k, v := range withSoftDeadline(nil, time.Date(2026, 9, 8, 21, 30, 0, 0, time.UTC)) {
		for _, r := range k + v {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
				t.Errorf("label %q=%q contains %q, which GCE rejects", k, v, r)
			}
		}
	}
}
