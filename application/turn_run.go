package application

import (
	"context"
	"sync"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// TurnRun tracks one streaming agent turn.
type TurnRun struct {
	ID             string
	ConversationID string
	MessageID      string
	Ctx            context.Context
	Cancel         context.CancelFunc
	// ProviderID is the resolved provider for this turn, used by the
	// dynamic 400-learning classifier to key learned param rules.
	ProviderID string
	// Headless marks unattended turns (pipeline agent steps). When true,
	// ACP subagent tools are filtered from the tool set so permission
	// prompts never stall a headless run.
	Headless bool
	// ToolKind overrides the ToolFactory agent kind for this run (empty =
	// default by Headless: conversation vs automation). Internal delegates
	// set AgentDelegate so the delegate tool itself is not advertised.
	ToolKind AgentKind
	// RiskTierCap is the maximum ACP RiskTier that may be promoted to
	// during a headless turn. Derived from the workflow TrustLevel via
	// domain.TrustLevelToRiskTierCap. Empty means no cap (interactive turns).
	RiskTierCap domain.RiskTier
	// Workspace is the absolute workspace root of the conversation, captured
	// at turn start so tool execution can attribute mutations without
	// re-reading the conversation.
	Workspace string

	messageMu   sync.RWMutex
	steerMu     sync.Mutex
	steerQueued *SteerEntry

	runDoneMu sync.Mutex
	runDone   []pendingRunDone

	// learningNodes records memory/skill IDs observed by successful tools in
	// this turn. Tool calls may run concurrently, so access is synchronized;
	// used_with edges are emitted later in deterministic persistence order.
	learningNodesMu sync.Mutex
	learningNodes   map[string]struct{}
}

// pendingRunDone is a finished background run waiting to be injected
// into the parent turn at the next steer-style tool-round boundary.
// Complete delivers the result into the parent conversation (patch the
// original tool call + inject the synthetic result message); producers
// are subagents today, delegates tomorrow.
type pendingRunDone struct {
	RunID    string
	Complete func(conversationID string) error
}

// SteerEntry is a user message queued for injection at the next tool round
// boundary while a turn is running.
type SteerEntry struct {
	ID      string
	Text    string
	Status  string // "queued" | "applied" | "cancelled"
	Message domain.Message
}

func (r *TurnRun) currentMessageID() string {
	r.messageMu.RLock()
	defer r.messageMu.RUnlock()
	return r.MessageID
}

func (r *TurnRun) setMessageID(id string) {
	if id == "" {
		return
	}
	r.messageMu.Lock()
	r.MessageID = id
	r.messageMu.Unlock()
}

// queueSteer stores a steer entry for this run. Returns false if a steer is
// already queued (only one at a time).
func (r *TurnRun) queueSteer(entry *SteerEntry) bool {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued != nil {
		return false
	}
	r.steerQueued = entry
	return true
}

// cancelSteerEntry removes a queued steer and returns it (with its text) so the
// caller can emit a cancel event that lets the frontend restore the draft to
// the composer. Returns nil if no queued steer exists.
func (r *TurnRun) cancelSteerEntry() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	r.steerQueued.Status = "cancelled"
	entry := r.steerQueued
	r.steerQueued = nil
	return entry
}

// drainSteer returns the queued steer entry and marks it applied, or nil if
// no steer is queued. Called by the agent loop at a safe boundary.
func (r *TurnRun) drainSteer() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	r.steerQueued.Status = "applied"
	entry := r.steerQueued
	r.steerQueued = nil
	return entry
}

// queuedSteer returns the current queued steer without consuming it.
func (r *TurnRun) queuedSteer() *SteerEntry {
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued == nil || r.steerQueued.Status != "queued" {
		return nil
	}
	return r.steerQueued
}

// requeueSteer puts a previously drained entry back when persist failed.
// Returns false if another steer occupied the slot.
func (r *TurnRun) requeueSteer(entry *SteerEntry) bool {
	if entry == nil {
		return false
	}
	r.steerMu.Lock()
	defer r.steerMu.Unlock()
	if r.steerQueued != nil {
		return false
	}
	entry.Status = "queued"
	r.steerQueued = entry
	return true
}

func (r *TurnRun) queueRunDone(entry pendingRunDone) {
	if r == nil || entry.Complete == nil {
		return
	}
	r.runDoneMu.Lock()
	defer r.runDoneMu.Unlock()
	r.runDone = append(r.runDone, entry)
}

func (r *TurnRun) drainRunDone() []pendingRunDone {
	if r == nil {
		return nil
	}
	r.runDoneMu.Lock()
	defer r.runDoneMu.Unlock()
	out := r.runDone
	r.runDone = nil
	return out
}

func (r *TurnRun) requeueRunDone(entries []pendingRunDone) {
	if r == nil || len(entries) == 0 {
		return
	}
	r.runDoneMu.Lock()
	defer r.runDoneMu.Unlock()
	r.runDone = append(entries, r.runDone...)
}

// newSteerEntry builds a queued steer with a persistable user message.
func newSteerEntry(text string, attachments []domain.Attachment) *SteerEntry {
	return &SteerEntry{
		ID:     domain.NewID(domain.IDPrefixSteer),
		Text:   text,
		Status: "queued",
		Message: domain.Message{
			ID:          domain.NewID(domain.IDPrefixMsg),
			Role:        domain.RoleUser,
			Content:     text,
			Attachments: attachments,
			CreatedAt:   clock.NewTime().Time(),
			Status:      domain.StatusDone,
			Steer:       true,
		},
	}
}
