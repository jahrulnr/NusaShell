package codex

import (
	"encoding/json"
)

// ResponseEvent is the decoded Responses SSE event union, mirroring the
// codex-api/src/common.rs ResponseEvent variants that matter for compaction
// and completion accounting. Untyped upstream variants (deltas, rate limits,
// safety buffering) surface as EventOther with their raw payload.
type ResponseEvent interface {
	isResponseEvent()
}

// EventCreated maps response.created. The response id is optional on the wire.
type EventCreated struct {
	ResponseID string
}

// EventOutputItemAdded maps response.output_item.added.
type EventOutputItemAdded struct {
	Item ResponseItem
}

// EventOutputItemDone maps response.output_item.done. Compaction output
// items arrive here.
type EventOutputItemDone struct {
	Item ResponseItem
}

// EventCompleted maps response.completed: the terminal event carrying the
// response id and best-effort usage. Usage is optional; without it, token
// accounting falls back to stale data and local estimates.
type EventCompleted struct {
	ResponseID    string
	TokenUsage    *TokenUsage
	UsageMetadata json.RawMessage
	EndTurn       *bool
}

// EventError maps response.failed and in-stream error events.
type EventError struct {
	Err error
}

// EventOther carries any recognized-but-unmodeled event, plus fully unknown
// event types, with the raw payload for observability.
type EventOther struct {
	Type string
	Raw  json.RawMessage
}

func (EventCreated) isResponseEvent()         {}
func (EventOutputItemAdded) isResponseEvent() {}
func (EventOutputItemDone) isResponseEvent()  {}
func (EventCompleted) isResponseEvent()       {}
func (EventError) isResponseEvent()           {}
func (EventOther) isResponseEvent()           {}
