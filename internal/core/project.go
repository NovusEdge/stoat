package core

import (
	"errors"

	"github.com/novusedge/stoat/internal/project"
)

// SpecFor turns one declaration into the Spec that Create takes.
func SpecFor(p *project.Project, key string) (Spec, error) {
	return Spec{}, errors.New("core: not implemented")
}
