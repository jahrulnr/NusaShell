package service

import "errors"

// Cancel causes name who ended a running job when the reason is neither the
// model's own doing nor a deadline. They travel on the context — set with
// context.WithCancelCause at the point the cancel is decided — so a tool
// whose process was stopped can say who stopped it instead of leaving a bare
// SIGKILL the model cannot read. One vocabulary, from the worker down to the
// tool result: "interrupted by user", "interrupted by agent", "nusashell
// restarted".
var (
	// ErrInterruptedByUser is the owner's own stop: the Stop button, or a
	// /cancel in any client.
	ErrInterruptedByUser = errors.New("interrupted by user")
	// ErrInterruptedByAgent is one call canceled by the agent that asked for
	// it — the agent's own steering cancel. No path sets it yet; it exists so
	// a tool result never has to borrow the owner's words for it later.
	ErrInterruptedByAgent = errors.New("interrupted by agent")
	// ErrServerRestarted is the server stopping the work it owns: shutdown,
	// restart, or a worker terminated on the way down.
	ErrServerRestarted = errors.New("nusashell restarted")
)
