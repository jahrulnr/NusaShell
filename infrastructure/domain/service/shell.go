package service

import (
	"context"
	"io"
)

// Shell is a program on a pseudo-terminal the owner works in — their
// login shell, or a command such as an agent's interactive sign-in: what
// it prints is read, what the owner types is written.
type Shell interface {
	io.ReadWriter
	Resize(cols, rows int) error
	// Wait blocks until the program exits and reports how it ended.
	Wait() error
	// Close ends the program and everything it started.
	Close() error
}

// ShellCommand is what a terminal runs. An empty Argv is the owner's
// login shell; otherwise Argv[0] is the program, found on PATH. Env sets
// variables on top of the server's own environment. An empty Dir is the
// owner's home folder.
type ShellCommand struct {
	Dir  string
	Argv []string
	Env  map[string]string
}

// Shells opens the owner's terminals.
type Shells interface {
	Open(ctx context.Context, cmd ShellCommand, cols, rows int) (Shell, error)
}
