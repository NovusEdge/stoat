// Package hostops owns the narrow qualification gate for native VM work.
package hostops

import "errors"

// ErrUnsupported reports a native host whose VM operations have not been
// qualified yet. Read-only metadata and diagnostics remain available there.
var ErrUnsupported = errors.New("native VM operations are not qualified")
