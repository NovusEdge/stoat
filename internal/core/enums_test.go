package core

import (
	"context"
	"errors"
	"testing"
)

// Wait refuses an Until it does not know, rather than falling into a default
// branch that silently means one of the three real ones.
func TestWaitRefusesAnUnknownUntil(t *testing.T) {
	root(t)
	err := Wait(context.Background(), "work", Until("nope"))
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("Wait(nope) = %v, want ErrInvalidSpec", err)
	}
}

// Logs refuses a Which it does not know. Before Valid(), the default branch
// read the apply log for any unrecognised selector.
func TestLogsRefusesAnUnknownWhich(t *testing.T) {
	root(t)
	if _, err := Logs("work", Which("nope")); !errors.Is(err, ErrUnknownWhich) {
		t.Errorf("Logs(nope) = %v, want ErrUnknownWhich", err)
	}
}

// The member lists are what a consumer generates a switch from, so each one
// holds exactly the declared values and calls them valid.
func TestMemberListsMatchTheDeclaredValues(t *testing.T) {
	if got, want := States(), []State{StateStopped, StateRunning, StateBroken}; !equalStates(got, want) {
		t.Errorf("States() = %v, want %v", got, want)
	}
	for _, u := range Untils() {
		if !u.Valid() {
			t.Errorf("Untils() lists %q, which Valid() rejects", u)
		}
	}
	for _, w := range Whichs() {
		if !w.Valid() {
			t.Errorf("Whichs() lists %q, which Valid() rejects", w)
		}
	}
	if got, want := Healths(), []Health{HealthOK, HealthFailed, HealthUnknown}; len(got) != len(want) {
		t.Errorf("Healths() = %v, want %v", got, want)
	}
}

func equalStates(a, b []State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
