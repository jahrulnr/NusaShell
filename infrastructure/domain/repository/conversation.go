package repository

import (
	"context"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ConversationInfo is a lightweight read projection for inspecting a
// conversation without parsing its message history — conversation lists,
// pagination headers, and inspection UIs consume it.
type ConversationInfo struct {
	ID         string
	Title      string
	ProviderID string
	ModelID    string
	// Effort and Upstream are the reasoning effort and aggregator backend
	// the owner picked for the room; empty = the model's / the provider's.
	Effort      string
	Upstream    string
	Workspace   string
	Environment enum.Environment
	// AgentID names the bound external agent; empty = the model runtime.
	AgentID string
	// ProjectID names the owning project; empty = none.
	ProjectID string
	Pinned    bool
	// MemoryOff keeps memory out of the room.
	MemoryOff bool
	// Label is the room's @handle; empty = none.
	Label string
	// AgentOptions are the owner's picks of the agent's session options,
	// in apply order.
	AgentOptions valueobject.ConfigSelections
	// AgentSession is the bound agent's own handle for the room's session;
	// empty when none was opened.
	AgentSession string
	// Routing is the room's reply routing override; nil = the default.
	Routing   *valueobject.Routing
	Usage     valueobject.Usage
	CreatedAt time.Time
	UpdatedAt time.Time
	// MessageCount is the total logical message count across sealed segments
	// plus the live tail; ActiveCount covers the live tail only — the slice
	// Load returns and the range most UI turns live in.
	MessageCount   int
	ActiveCount    int
	SealedSegments int
}

// PendingSelection is a room's runtime pick and session-option picks waiting for
// the room's next turn: a selection is never applied mid-turn (a turn's runtime
// is fixed when it starts), so the pick waits in its own file beside the record
// and the next turn applies it before anything else runs. A turn's own Save
// rewrites the whole record, which is why this cannot be a field there.
//
// One selection per room, last pick wins per field: a new runtime replaces the
// other runtime's fields (an agent pick clears the provider pair and the
// reverse), effort and upstream replace their own value, and options merge by
// id — a later pick for the same option wins.
type PendingSelection struct {
	AgentID    string            `json:"agentId,omitempty"`
	AgentModel string            `json:"agentModel,omitempty"`
	ProviderID string            `json:"providerId,omitempty"`
	ModelID    string            `json:"modelId,omitempty"`
	Effort     *enum.Effort      `json:"effort,omitempty"`
	Upstream   *string           `json:"upstream,omitempty"`
	Options    map[string]string `json:"options,omitempty"`
	// AcceptedAt is when the owner's latest pick was taken — the answer a 202
	// carries, and what a surface shows a returning owner.
	AcceptedAt time.Time `json:"acceptedAt"`
}

// Empty reports that no pick survives in the selection — the one definition
// of emptiness, so a store dropping superseded fields and any future reader
// never disagree about what "nothing parked" means. AcceptedAt is metadata,
// not a pick: a generation stamp alone does not make a selection non-empty.
func (s PendingSelection) Empty() bool {
	return s.AgentID == "" && s.AgentModel == "" && s.ProviderID == "" && s.ModelID == "" &&
		s.Effort == nil && s.Upstream == nil && len(s.Options) == 0
}

// Covered names which parked fields an explicit idle pick supersedes: the
// fields it just applied, never anything else — a pick that covers only the
// runtime must leave parked options waiting with their notice intact.
type Covered struct {
	Runtime  bool
	Effort   bool
	Upstream bool
	Options  []string
}

// Conversation is the conversation store port. Ordering contract: Save
// creates/upserts the record and must precede AppendMessage. Errors carry
// the apperror contract — a missing convID surfaces as 404, distinct from
// corruption or I/O failures.
type Conversation interface {
	// Save upserts the conversation-level record: id, providerID/modelID binding,
	// title, system prompt, tools catalog, accumulated usage, and the room's
	// label. Messages are persisted per turn via AppendMessage. The bound
	// provider is a separate aggregate — resolved via the Provider repository
	// by ProviderID().
	//
	// Save owns the label invariant: a new room with no label is given the
	// first one no other room holds, and a label another room already holds is
	// refused (409) — both decided under the lock that writes the record, so
	// concurrent creations and renames cannot end up sharing a handle. The
	// aggregate's label is updated to the label stored.
	Save(ctx context.Context, conv *aggregate.Conversation) error

	// AppendMessage appends one message (a turn) to the stored history. The
	// bool reports whether the message line committed even if a later metadata
	// projection update fails.
	AppendMessage(ctx context.Context, convID string, msg entity.Message) (persisted bool, err error)

	// UpdateToolCallOutput patches the stored output of a previously
	// appended tool call — a call is persisted pending execution, and its
	// raw output, fixed latency, and any media references it
	// produced arrive later. Each call is an independent patch: concurrent
	// completions of parallel tool calls are safe by construction
	// (implementations append patch records rather than rewriting stored
	// lines), and a crash mid-batch leaves the remaining calls honestly
	// pending instead of losing every result. callID resolves globally
	// across segments: a call compacted before its output arrived is still
	// patched correctly.
	UpdateToolCallOutput(ctx context.Context, convID string, call entity.ToolCall) error

	// RecordDelivery records a reply's trip to a chat on message msgID:
	// files that went with it and a receipt (see Message.RecordDelivery).
	// Append-only like UpdateToolCallOutput; a message that is gone from
	// the live history ignores it.
	RecordDelivery(ctx context.Context, convID, msgID string, files []valueobject.Attachment, receipt valueobject.Receipt) error

	// Compact seals messages[:dropBefore] into the next numbered archive
	// segment and restarts the live history as [summary] + retained —
	// mirroring Conversation.Compact, which the caller must apply to the
	// aggregate as well. retained must be exactly the live tail
	// (dropBefore onward) in order, with any replay artifacts the provider
	// no longer accepts stripped from optionalContent. Callers must not
	// split a tool-call/result pair at the boundary.
	Compact(ctx context.Context, convID string, summary entity.Message, dropBefore int, retained []entity.Message) error

	// RewriteFrom is the edit primitive — the persistence counterpart of
	// Conversation.RewriteFrom. It locates a user message anywhere in
	// history, rewrites its content, and discards every message recorded
	// after it. A target in the live tail truncates active.jsonl. A target
	// in a sealed segment promotes that segment — plus every earlier one —
	// back into active.jsonl so the model regains the full context of that
	// era; later segments and the old active tail are deleted. 404 when the
	// id is nowhere in history, 400 when it belongs to a non-user message.
	RewriteFrom(ctx context.Context, convID, msgID, content string) error

	// SavePendingSelection parks the room's runtime and session-option picks
	// for its next turn, merging with whatever already waits there: the last
	// pick wins per field, and a new runtime replaces the other one's fields.
	// It must not be applied to the record now — the turn already running owns
	// that save, and the pick would be lost or halfway applied.
	SavePendingSelection(ctx context.Context, convID string, sel PendingSelection) error

	// PendingSelection reads what waits, reporting false when nothing does.
	PendingSelection(ctx context.Context, convID string) (PendingSelection, bool, error)

	// ClearPendingSelection drops the pick a turn consumed and saved, identified
	// by the acceptedAt it applied. A newer pick that arrived while that turn ran
	// is left alone: it has not been applied yet, and removing the file would lose
	// it silently.
	ClearPendingSelection(ctx context.Context, convID string, acceptedAt time.Time) error
	// SupersedePendingSelection drops only the pending fields an explicit idle
	// pick just applied, so a parked older generation never folds over the
	// newer value at the next turn. Fields the pick does not cover stay parked.
	SupersedePendingSelection(ctx context.Context, convID string, cover Covered) error

	// SaveCompletions parks the asynchronous finishes a room has not read yet
	// beside its record, merging with what already waits: a finish whose
	// source+id is already known is the same exit reported twice and is not
	// repeated. The write is atomic and survives the process that reported it —
	// a restart between the exit and the turn that reads it must not lose it
	// silently. The bool reports whether the finish was recorded now (false for
	// a repeat), so the caller wakes only for news.
	SaveCompletions(ctx context.Context, convID string, list []valueobject.Completion) (bool, error)

	// PendingCompletions reads the finishes the room has not read, oldest first.
	PendingCompletions(ctx context.Context, convID string) ([]valueobject.Completion, error)

	// ClearCompletions drops the records a turn rendered, identified by
	// Completion.Key. The keys stay known, so a producer that replays an exit
	// already read does not park it again.
	ClearCompletions(ctx context.Context, convID string, keys []string) error

	// DropCompletions removes a deleted room's pending list with the room: no
	// record outlives the room it belonged to.
	DropCompletions(ctx context.Context, convID string) error

	// Info returns the lightweight projection for one conversation.
	Info(ctx context.Context, convID string) (ConversationInfo, error)

	// List returns infos for all stored conversations, newest activity
	// first — backs the conversation list UI.
	List(ctx context.Context) ([]ConversationInfo, error)

	// Load rebuilds the conversation's LIVE context: providerID/modelID bindings
	// plus the active segment — the messages the model actually sees. Sealed
	// archive segments stay on disk; LoadAt reaches them. The Provider and
	// Model objects are resolved separately — repositories may span
	// multiple aggregates.
	Load(ctx context.Context, convID string) (*aggregate.Conversation, error)

	// LoadAt returns messages[start:end] over the global logical history —
	// sealed segments first, then the live tail — in append order. Indexes
	// clamp to bounds; a negative start counts from the end
	// (LoadAt(id, -10, 0) reads the latest 10), and end <= 0 reads through
	// the most recent message.
	LoadAt(ctx context.Context, convID string, start, end int) ([]entity.Message, error)

	// Delete removes the whole conversation and its owned files, including
	// tool output, while leaving other conversations untouched.
	Delete(ctx context.Context, convID string) error

	// Clear empties the room's stored dialogue — the live tail, every sealed
	// segment, and the conversation-owned tool output — and rewrites meta
	// from conv, the caller's post-state: identity kept, counters zero, and
	// the agent session dropped (see Conversation.ClearHistory). conv carries
	// the same id; the messages it holds are not written.
	Clear(ctx context.Context, conv *aggregate.Conversation) error

	// Fork copies srcID's logical history — sealed segments plus the live
	// tail, with the message attachments and tool-output files they
	// reference — into a new room described by dst, which supplies the new id
	// and its bindings. The copy is published only once it is complete, so
	// Info, List, and Load never find a half-copied thread. The source is
	// read, never modified.
	Fork(ctx context.Context, srcID string, dst *aggregate.Conversation) error
}

// RepairAction selects how RepairOrphan resolves an uncommitted sealed
// segment: discard it as a duplicate of the live tail, or commit it as
// recovered history.
type RepairAction string

const (
	RepairDiscardDuplicate RepairAction = "discard-duplicate"
	RepairCommit           RepairAction = "commit"
)

func (a RepairAction) String() string { return string(a) }

// ConversationRepairer is the offline recovery port for the conversation
// store — separate from Conversation on purpose: repair rewrites storage
// invariants that runtime operations assume hold, so jobs and brokers
// holding Conversation must never see the verb. Only operator tooling
// obtains it, and only while no other process uses the store.
type ConversationRepairer interface {
	// RepairOrphan reconciles the one extra sealed segment an interrupted
	// Compact can leave between publishing the segment and its metadata
	// counter: RepairDiscardDuplicate removes a segment whose messages
	// still lead the live tail; RepairCommit publishes a unique one and
	// recomputes the stored counters. Callers stop the application first.
	RepairOrphan(ctx context.Context, convID string, action RepairAction) error

	// RepairRewrite finishes a sealed-history edit interrupted mid-commit:
	// the durable payload the edit staged is validated, published over the
	// live tail, and the parked and numbered leftovers removed before the
	// counters are recalculated. Rolling forward is the only direction —
	// the merged stream already carries the surviving copy of the parked
	// eras. Callers stop the application first.
	RepairRewrite(ctx context.Context, convID string) error
}
