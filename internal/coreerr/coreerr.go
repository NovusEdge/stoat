// Package coreerr holds the sentinel errors internal/core and
// internal/capabilities both need to identify by identity (errors.Is).
// capabilities cannot import core directly: core imports provider, provider
// imports capabilities, and a capabilities->core edge would close that
// cycle. core re-exports these under its own names so every existing
// core.ErrXxx caller compiles unchanged.
package coreerr

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrInvalidSpec = errors.New("invalid spec")
	ErrBroken      = errors.New("broken vm.toml")
)
