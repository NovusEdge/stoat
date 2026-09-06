package core

import (
	"errors"
	"testing"
)

func TestGuestsSorted(t *testing.T) {
	gs := Guests()
	want := []string{"almalinux", "alpine", "arch", "debian", "fedora", "opensuse", "rocky", "ubuntu"}
	if len(gs) != len(want) {
		t.Fatalf("got %d guests, want %d: %v", len(gs), len(want), gs)
	}
	for i, name := range want {
		if gs[i].Name != name {
			t.Errorf("guest[%d] = %q, want %q", i, gs[i].Name, name)
		}
	}
}

func TestGuestUnknownIsNotFound(t *testing.T) {
	if _, err := Guest("plan9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
