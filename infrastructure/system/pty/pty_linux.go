//go:build linux

// Package pty allocates pseudo-terminals for the processes NusaShell runs
// interactively: the model's TTY tool sessions and the owner's terminal.
package pty

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// Open allocates a pseudo-terminal pair: master stays with the parent for
// reads and writes, slave goes to the child. Unlock via TIOCSPTLCK,
// resolve the number via TIOCGPTN, open /dev/pts/N without making it the
// caller's controlling terminal. The pair starts at 80x24.
func Open() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(e error) (*os.File, *os.File, error) {
		master.Close()
		return nil, nil, e
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return fail(err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return fail(err)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	// Best effort: programs querying winsize get an answer instead of 0x0.
	_ = Resize(master, 80, 24)
	return master, slave, nil
}

// Resize sets the terminal's size; the kernel signals SIGWINCH.
func Resize(master *os.File, cols, rows int) error {
	return unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)})
}
