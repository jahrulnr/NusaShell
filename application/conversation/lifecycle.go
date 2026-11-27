package appconversation

import (
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// Lifecycle words. The server writes exactly one of these on every
// conversation projection. Absent, on a client, means unknown — idle is
// only a turn that completed.
const (
	LifecycleRunning     = "running"
	LifecycleApproval    = "approval"
	LifecycleInterrupted = "interrupted"
	LifecycleFailed      = "failed"
	LifecycleIdle        = "idle"
	LifecycleUnknown     = "unknown"
)

// Stored terminal words. They are not lifecycle words: completed is the
// only one that becomes idle, and only when no turn is live.
const (
	TerminalCompleted   = "completed"
	TerminalFailed      = "failed"
	TerminalInterrupted = "interrupted"
)

// Lifecycle applies the thread-lifecycle order. live is a turn in progress.
// waitingOnOwner is that live turn blocked on an approval or an elicitation;
// a leftover request from a turn that has ended does not count, because the
// caller only sets it while live. readable is false when the last terminal
// cannot be read. terminal is the stored outcome, or "" when none is stored.
//
// First match: approval, running, then — only with no live turn — unknown
// when the terminal is missing or unreadable, interrupted, failed, and idle
// solely for a completed terminal. interrupted and an unreadable terminal
// never become idle.
func Lifecycle(live, waitingOnOwner, readable bool, terminal string) string {
	if live && waitingOnOwner {
		return LifecycleApproval
	}
	if live {
		return LifecycleRunning
	}
	if !readable {
		return LifecycleUnknown
	}
	switch terminal {
	case TerminalInterrupted:
		return LifecycleInterrupted
	case TerminalFailed:
		return LifecycleFailed
	case TerminalCompleted:
		return LifecycleIdle
	default:
		return LifecycleUnknown
	}
}

// TerminalFromMessages reads the last assistant line's finish reason. A
// stop, a tool request, a length cut, or a content filter is a completed
// turn. interrupted is a stop. Anything else — including an assistant line
// with no reason — is not a stored terminal.
func TerminalFromMessages(msgs []entity.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role() != enum.RoleAssistant {
			continue
		}
		switch msgs[i].FinishReason() {
		case enum.FinishReasonInterrupted:
			return TerminalInterrupted
		case enum.FinishReasonStop, enum.FinishReasonToolCalls, enum.FinishReasonLength, enum.FinishReasonContentFilter:
			return TerminalCompleted
		default:
			return ""
		}
	}
	return ""
}

// ResolveTerminal prefers a stored word. When nothing is stored it uses the
// messages, and a history error means the terminal cannot be read.
func ResolveTerminal(stored string, msgs []entity.Message, histErr error) (terminal string, readable bool) {
	if stored != "" {
		return stored, true
	}
	if histErr != nil {
		return "", false
	}
	return TerminalFromMessages(msgs), true
}
