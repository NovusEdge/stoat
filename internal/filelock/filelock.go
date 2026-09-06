package filelock

import (
	"errors"
)

type Mode uint8

const (
	Shared Mode = iota
	Exclusive
)

var ErrWouldBlock = errors.New("file lock would block")
