package core

import (
	"errors"
	"fmt"

	"github.com/novusedge/stoat/internal/guest"
)

// ErrUnknownGuest marks a vm.toml whose os names no loaded guest. It is
// wrapped by ErrBroken so List and Get show the VM instead of hiding it.
var ErrUnknownGuest = errors.New("unknown guest")

// Guests returns every loaded guest, sorted by name.
func Guests() []guest.OS { return guest.All() }

// Guest returns one guest by name.
func Guest(name string) (guest.OS, error) {
	o, ok := guest.Lookup(name)
	if !ok {
		return guest.OS{}, fmt.Errorf("%w: guest %s", ErrNotFound, name)
	}
	return o, nil
}
