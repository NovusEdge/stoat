//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func Lock(f *os.File, mode Mode, nonBlocking bool) error {
	var flags uint32
	if mode == Exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if nonBlocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrWouldBlock
	}
	return err
}

func Unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
}
