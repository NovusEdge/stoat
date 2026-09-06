package filelock

import (
	"errors"
	"os"
)

type Mode uint8

const (
	Shared Mode = iota
	Exclusive
)

var ErrWouldBlock = errors.New("file lock would block")

func Lock(*os.File, Mode, bool) error {
	return errors.New("not implemented")
}

func Unlock(*os.File) error {
	return errors.New("not implemented")
}
