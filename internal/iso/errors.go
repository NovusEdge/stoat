package iso

import "errors"

// Sentinels for the failures a caller branches on. A stalled download and a
// checksum mismatch are both "the image is not here", but the first is worth
// retrying on the same mirror and the second is not.
var (
	ErrDownloadFailed   = errors.New("download failed")
	ErrDownloadStalled  = errors.New("download stalled")
	ErrChecksumMismatch = errors.New("checksum mismatch")
	ErrNoSuchImage      = errors.New("no such image")
)
