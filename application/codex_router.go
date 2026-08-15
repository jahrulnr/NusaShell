package application

import (
	"sync"
	"time"
)

// CodexAccountRouter manages sticky account-to-conversation mapping,
// failover on rate limits, and per-account circuit breaking for Codex
// providers.
//
// Sticky: once a conversation starts using account X, subsequent rounds
// in the same conversation keep using X so the Codex backend can reuse
// the prompt cache shard for that account.
//
// Failover: when an account hits a 429, it is marked rate-limited for
// a cooldown period. The next PickAccount call will skip it and return
// a different available account. The sticky mapping is updated so
// subsequent rounds use the new account.
//
// Circuit breaker: accounts that exhaust their usage quota (long
// Retry-After or FetchUsage reports LimitReached) are circuit-open
// until their usage window resets. PickAccount skips circuit-open
// accounts just like rate-limited ones, but the block duration is
// typically hours instead of minutes.
//
// All state is in-memory and resets on restart. This is intentional:
// the sticky mapping only matters within a session, and the provider
// will re-inform us of rate limits if they persist.
type CodexAccountRouter struct {
	mu          sync.Mutex
	sticky      map[string]string    // conversationID → accountID
	cooldown    map[string]time.Time // accountID → transient rate-limited until
	circuitOpen map[string]time.Time // accountID → usage exhausted until
}

// NewCodexAccountRouter creates a ready-to-use router.
func NewCodexAccountRouter() *CodexAccountRouter {
	return &CodexAccountRouter{
		sticky:      map[string]string{},
		cooldown:    map[string]time.Time{},
		circuitOpen: map[string]time.Time{},
	}
}

// PickAccountResult holds the result of an account pick, including
// metadata about why no account was available (all rate-limited or
// circuit-open).
type PickAccountResult struct {
	AccountID string
	// AllRateLimited is true when no account was available because all
	// are either transient rate-limited or circuit-open.
	AllRateLimited bool
	// EarliestReset is the earliest time any blocked account becomes
	// available again. Zero if AllRateLimited is false.
	EarliestReset time.Time
}

// PickAccount returns the best account ID for the given conversation.
// If the conversation has a sticky account that is not blocked, returns it.
// Otherwise picks the first available (non-blocked) account and makes it
// sticky. Returns "" if no accounts are available.
func (r *CodexAccountRouter) PickAccount(conversationID, providerID string, available []string) string {
	res := r.PickAccountDetailed(conversationID, providerID, available)
	return res.AccountID
}

// PickAccountDetailed is like PickAccount but also reports why no account
// was picked when AccountID is empty. Used by the failover path to produce
// a clear error message to the user.
func (r *CodexAccountRouter) PickAccountDetailed(conversationID, providerID string, available []string) PickAccountResult {
	if len(available) == 0 {
		return PickAccountResult{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	// Build a set of available accounts for O(1) sticky lookup
	availSet := make(map[string]bool, len(available))
	for _, acc := range available {
		availSet[acc] = true
	}
	// Check sticky first — but only if it's still in the available list.
	// If the sticky account was deleted, clean up the stale mapping.
	if sticky, ok := r.sticky[conversationID]; ok {
		if !availSet[sticky] {
			delete(r.sticky, conversationID)
		} else if !r.isBlockedLocked(sticky, now) {
			return PickAccountResult{AccountID: sticky}
		}
	}
	// Pick first available that is not blocked
	for _, acc := range available {
		if !r.isBlockedLocked(acc, now) {
			r.sticky[conversationID] = acc
			return PickAccountResult{AccountID: acc}
		}
	}
	// All blocked — find earliest reset time
	earliest := r.earliestUnblockLocked(available, now)
	return PickAccountResult{
		AllRateLimited: true,
		EarliestReset:  earliest,
	}
}

// StickyAccount returns the currently sticky account for a conversation,
// or "" if none. Does NOT check rate-limit status — used for logging
// and failover bookkeeping.
func (r *CodexAccountRouter) StickyAccount(conversationID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sticky[conversationID]
}

// MarkRateLimited marks an account as transiently rate-limited for the
// given cooldown duration (typically seconds to minutes). Subsequent
// PickAccount calls will skip this account until the cooldown expires.
func (r *CodexAccountRouter) MarkRateLimited(accountID string, cooldown time.Duration) {
	if accountID == "" || cooldown <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cooldown[accountID] = time.Now().Add(cooldown)
}

// MarkCircuitOpen marks an account as circuit-open (usage exhausted)
// until the given time. This is a longer-term block than MarkRateLimited
// — typically hours, corresponding to the account's usage window reset.
// Subsequent PickAccount calls will skip this account until the circuit
// closes (time passes) or ResetCircuit is called.
func (r *CodexAccountRouter) MarkCircuitOpen(accountID string, until time.Time) {
	if accountID == "" || until.IsZero() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Don't shorten an existing circuit-open window
	if existing, ok := r.circuitOpen[accountID]; ok && existing.After(until) {
		return
	}
	r.circuitOpen[accountID] = until
}

// ResetCircuit manually closes the circuit breaker for an account.
// Used when the user manually triggers a retry or when a successful
// request confirms the account is healthy again.
func (r *CodexAccountRouter) ResetCircuit(accountID string) {
	if accountID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.circuitOpen, accountID)
	delete(r.cooldown, accountID)
}

// CircuitOpenUntil returns the time at which the account's circuit
// breaker will close, or zero if the circuit is not open. Used for
// logging and UI status.
func (r *CodexAccountRouter) CircuitOpenUntil(accountID string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	until, ok := r.circuitOpen[accountID]
	if !ok {
		return time.Time{}
	}
	if time.Now().After(until) {
		delete(r.circuitOpen, accountID)
		return time.Time{}
	}
	return until
}

// isBlockedLocked reports whether an account is blocked by either a
// transient cooldown or a circuit breaker. Caller must hold r.mu.
func (r *CodexAccountRouter) isBlockedLocked(accountID string, now time.Time) bool {
	return r.isCoolingDownLocked(accountID, now) || r.isCircuitOpenLocked(accountID, now)
}

// isCoolingDownLocked reports whether an account is still in its transient
// rate-limit cooldown. Caller must hold r.mu.
func (r *CodexAccountRouter) isCoolingDownLocked(accountID string, now time.Time) bool {
	until, ok := r.cooldown[accountID]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(r.cooldown, accountID)
		return false
	}
	return true
}

// isCircuitOpenLocked reports whether an account's circuit breaker is
// open (usage exhausted). Caller must hold r.mu.
func (r *CodexAccountRouter) isCircuitOpenLocked(accountID string, now time.Time) bool {
	until, ok := r.circuitOpen[accountID]
	if !ok {
		return false
	}
	if now.After(until) {
		delete(r.circuitOpen, accountID)
		return false
	}
	return true
}

// earliestUnblockLocked returns the earliest time any of the given
// accounts becomes unblocked, or zero if none are blocked. Caller must
// hold r.mu.
func (r *CodexAccountRouter) earliestUnblockLocked(accounts []string, now time.Time) time.Time {
	var earliest time.Time
	for _, acc := range accounts {
		if t, ok := r.cooldown[acc]; ok && t.After(now) {
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
		if t, ok := r.circuitOpen[acc]; ok && t.After(now) {
			if earliest.IsZero() || t.Before(earliest) {
				earliest = t
			}
		}
	}
	return earliest
}
