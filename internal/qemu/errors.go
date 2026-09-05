package qemu

import "errors"

// Sentinels for the failures a caller branches on. Each is wrapped in front
// of the message the call site already printed, so the text is unchanged and
// errors.Is reaches the sentinel through core's own wrapping.
//
// ErrNoXattr lives in xattr.go, beside the probe that raises it.
//
// ErrAlreadyRunning duplicates core.ErrAlreadyRunning by necessity: core
// imports qemu, so qemu cannot name core's sentinel. wire maps both to
// already_running.
var (
	ErrBinaryMissing      = errors.New("qemu binary not found")
	ErrKVMUnusable        = errors.New("/dev/kvm not usable")
	ErrStartFailed        = errors.New("qemu failed to start")
	ErrMonitorUnreachable = errors.New("qemu monitor unreachable")
	// ErrMonitorRejected is qemu answering a command with an error object.
	// The command reached qemu; qemu refused it.
	ErrMonitorRejected   = errors.New("qemu monitor rejected the command")
	ErrNoConsolePassword = errors.New("no console password set")
	ErrShareInvalid      = errors.New("share is not a directory")
	ErrAlreadyRunning    = errors.New("already running")
	ErrScreenshotFailed  = errors.New("screenshot failed")
	// ErrNotRunning duplicates core.ErrNotRunning: core imports qemu, so
	// qemu cannot name core's sentinel. wire maps both to not_running.
	ErrNotRunning = errors.New("not running")
)
