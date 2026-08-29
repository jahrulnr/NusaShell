package domain

import (
	"strings"
	"unicode"
)

// AutoContinueReason explains why the chain continues or stops.
type AutoContinueReason string

const (
	AutoContinueContinue           AutoContinueReason = "continue"
	AutoContinueAwaitingBackground AutoContinueReason = "awaiting-background-jobs"
	AutoContinueAwaitingUser       AutoContinueReason = "awaiting-user"
	AutoContinueNoOpenTodos        AutoContinueReason = "no-open-todos"
	AutoContinueMaxReached         AutoContinueReason = "max-reached"
	AutoContinueTurnNotOK          AutoContinueReason = "turn-not-ok"
	AutoContinueNoConversation     AutoContinueReason = "no-conversation"
)

// AutoContinueDecision is the outcome of the outer multi-turn auto-continue
// policy. When ShouldContinue is true, the agent runner starts the next turn
// without a user message, injecting the `announcement` tool result with the
// continuation guidance.
type AutoContinueDecision struct {
	ShouldContinue   bool               `json:"should_continue"`
	OpenTodoCount    int                `json:"open_todo_count"`
	ContinuesUsed    int                `json:"continues_used"`
	MaxAutoContinues int                `json:"max_auto_continues"`
	Reason           AutoContinueReason `json:"reason"`
}

// AutoContinueInput is the input to DecideAutoContinue.
type AutoContinueInput struct {
	Items             []TodoItem
	AutoContinueIndex int    // 0 = user-started turn; N > 0 = Nth auto-continue that just finished
	MaxAutoContinues  int    // 0 = unlimited; negative = default
	TurnOK            bool   // did the just-finished turn succeed?
	HasConversation   bool   // without a conversation the chain has no todo SoT
	TurnText          string // final visible text; a question means the agent is waiting for the user
	HasBackgroundJobs bool   // a long-running async tool owns the next state transition
}

// DecideAutoContinue is the pure multi-turn auto-continue policy
// (Codex-inspired outer loop).
//
// Open todos = items whose status is pending or in_progress. The chain
// continues only when the turn succeeded, open todos remain, the final
// text does not end with a question, no background jobs are running,
// and the chain has not exhausted MaxAutoContinues.
func DecideAutoContinue(input AutoContinueInput) AutoContinueDecision {
	maxAutoContinues := NormalizeMaxAutoContinues(input.MaxAutoContinues)
	continuesUsed := input.AutoContinueIndex
	if continuesUsed < 0 {
		continuesUsed = 0
	}
	openTodoCount := CountOpenTodos(input.Items)

	if !input.HasConversation {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueNoConversation}
	}
	if !input.TurnOK {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueTurnNotOK}
	}
	if endsWithQuestion(input.TurnText) {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueAwaitingUser}
	}
	if openTodoCount == 0 {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueNoOpenTodos}
	}
	if input.HasBackgroundJobs {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueAwaitingBackground}
	}
	// 0 = unlimited: skip the budget check entirely.
	if maxAutoContinues > 0 && continuesUsed >= maxAutoContinues {
		return AutoContinueDecision{ShouldContinue: false, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueMaxReached}
	}
	return AutoContinueDecision{ShouldContinue: true, OpenTodoCount: openTodoCount, ContinuesUsed: continuesUsed, MaxAutoContinues: maxAutoContinues, Reason: AutoContinueContinue}
}

// endsWithQuestion reports whether text ends with a "?" (ASCII or fullwidth).
// Trailing whitespace is ignored.
func endsWithQuestion(text string) bool {
	trimmed := strings.TrimRightFunc(text, unicode.IsSpace)
	if trimmed == "" {
		return false
	}
	r := []rune(trimmed)
	last := r[len(r)-1]
	return last == '?' || last == '？'
}

// NormalizeMaxAutoContinues clamps the auto-continue budget.
//
//   - negative → product default (10).
//   - 0 → unlimited sentinel (kept as 0; DecideAutoContinue skips the
//     budget check). This is the opt-in escape hatch for long unattended runs.
//   - 1..Cap → finite ceiling.
//   - > Cap → clamped to Cap.
func NormalizeMaxAutoContinues(value int) int {
	if value < 0 {
		return DefaultMaxAutoContinues
	}
	if value == 0 {
		return 0
	}
	if value > MaxAutoContinuesCap {
		return MaxAutoContinuesCap
	}
	return value
}
