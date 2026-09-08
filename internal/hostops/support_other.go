//go:build !linux

package hostops

import "runtime"

// RequireLocalHypervisor refuses native VM operations until this host
// platform has a complete runtime qualification. The message names what is
// missing, what still works, and where the work is tracked, so the refusal
// is actionable without a doc lookup.
func RequireLocalHypervisor() error {
	return unsupportedError{Message(runtime.GOOS, runtime.GOARCH)}
}

// RequireDataRoot permits owning a data root on any host. See the Linux
// implementation for why this is not gated.
func RequireDataRoot() error { return nil }

// unsupportedError carries Message's text as Error() while still satisfying
// errors.Is(err, ErrUnsupported): fmt.Errorf("%w: %s", ...) would duplicate
// Message's own "not qualified on <host>" opening line.
type unsupportedError struct{ msg string }

func (e unsupportedError) Error() string        { return e.msg }
func (e unsupportedError) Is(target error) bool { return target == ErrUnsupported }
