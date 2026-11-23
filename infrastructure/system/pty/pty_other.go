//go:build !linux

package pty

import (
	"errors"
	"os"
)

// ErrUnsupported: pseudo-terminals are allocated on Linux only.
var ErrUnsupported = errors.New("terminals are unsupported on this platform")

func Open() (master, slave *os.File, err error) { return nil, nil, ErrUnsupported }

func Resize(*os.File, int, int) error { return ErrUnsupported }
