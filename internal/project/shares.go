package project

import "errors"

// Share is one project directory exposed to a VM over 9p.
type Share struct {
	Tag   string
	Host  string
	Guest string
}

// Shares resolves one declaration's shares entries to absolute host paths and
// guest mountpoints.
func (p *Project) Shares(key string) ([]Share, error) {
	return nil, errors.New("project: not implemented")
}
