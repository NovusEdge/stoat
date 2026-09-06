//go:build !linux

package hostops

import (
	"fmt"
	"runtime"
)

// RequireVM refuses native VM operations until this host platform has a
// complete runtime qualification. The host identity makes the diagnostic
// actionable when the same binary is moved between platforms.
func RequireVM() error {
	return fmt.Errorf("%w on %s/%s", ErrUnsupported, runtime.GOOS, runtime.GOARCH)
}
