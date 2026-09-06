//go:build windows

package qemu

import "github.com/novusedge/stoat/internal/hostops"

// xattrOK is unavailable until Windows share semantics are qualified. The
// start guard rejects before this probe could be reached.
func xattrOK(string) error { return hostops.ErrUnsupported }
