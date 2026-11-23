package valueobject

import (
	"errors"
	"math"
	"strings"
)

// AgentContext is what an external agent reports about its own session: how
// many tokens its context window holds now, how large the window is, and,
// when the agent prices its work, what the session has cost so far. It
// describes one session at one moment — a later report replaces it, and it
// is not the running token total Usage accumulates.
type AgentContext struct {
	used         int
	size         int
	costAmount   float64
	costCurrency string
}

// NewAgentContext validates a report. A report without a window size says
// nothing about how full the context is, so it is refused; used may exceed
// size, which an agent that has not compacted yet can report. A cost is
// kept only with its currency.
func NewAgentContext(used, size int, costAmount float64, costCurrency string) (AgentContext, error) {
	costCurrency = strings.ToUpper(strings.TrimSpace(costCurrency))
	switch {
	case used < 0:
		return AgentContext{}, errors.New("agent context used cannot be negative")
	case size <= 0:
		return AgentContext{}, errors.New("agent context needs a window size")
	case math.IsNaN(costAmount) || math.IsInf(costAmount, 0) || costAmount < 0:
		return AgentContext{}, errors.New("agent context cost must be a non-negative number")
	case len(costCurrency) > 8:
		return AgentContext{}, errors.New("agent context cost currency is too long")
	}
	if costCurrency == "" {
		costAmount = 0
	}
	return AgentContext{used: used, size: size, costAmount: costAmount, costCurrency: costCurrency}, nil
}

// Used is the tokens the session's context holds.
func (a AgentContext) Used() int { return a.used }

// Size is the session's context window in tokens.
func (a AgentContext) Size() int { return a.size }

// Cost is the session's cost so far; ok is false when the agent reported none.
func (a AgentContext) Cost() (amount float64, currency string, ok bool) {
	return a.costAmount, a.costCurrency, a.costCurrency != ""
}

// IsZero reports whether the agent has reported nothing.
func (a AgentContext) IsZero() bool { return a.size == 0 }
