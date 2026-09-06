//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

func Lock(f *os.File, mode Mode, nonBlocking bool) error {
	flags := syscall.LOCK_SH
	if mode == Exclusive {
		flags = syscall.LOCK_EX
	}
	if nonBlocking {
		flags |= syscall.LOCK_NB
	}
	err := syscall.Flock(int(f.Fd()), flags)
	if nonBlocking && errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrWouldBlock
	}
	return err
}

func Unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
