package core

import (
	"errors"

	"github.com/novusedge/stoat/internal/guest"
)

// ErrUnknownGuest marks a vm.toml whose os names no loaded guest.
var ErrUnknownGuest = errors.New("unknown guest")

// Guests returns every loaded guest, sorted by name.
func Guests() []guest.OS { return nil }

// Guest returns one guest by name.
func Guest(name string) (guest.OS, error) { return guest.OS{}, nil }
