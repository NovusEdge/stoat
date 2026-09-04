package core

import (
	"errors"
	"testing"
)

func TestGuestsSorted(t *testing.T) {
	gs := Guests()
	if len(gs) < 5 || gs[0].Name != "alpine" || gs[1].Name != "arch" {
		t.Errorf("got %d guests, first two %q %q", len(gs), gs[0].Name, gs[1].Name)
	}
}

func TestGuestUnknownIsNotFound(t *testing.T) {
	if _, err := Guest("plan9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
