package service

import (
	"context"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
)

type TextModelService interface {
	// Send performs one completion attempt against prov, appending the
	// resulting assistant message and usage onto conv. Implementations are
	// stateless translators — endpoint and credentials are read from prov per
	// call, so a re-Loaded provider (rotated key, moved endpoint) takes
	// effect immediately. Model and provider selection — including fallback
	// — belong to the application layer.
	//
	// In stream mode onEvent receives domain events as they arrive (deltas,
	// completed tool calls, finished/error). In non-stream mode the
	// implementation emits a single delta carrying the full content followed
	// by finished, so callers consume both modes uniformly. onEvent may be
	// nil.
	//
	// Failures return apperror.IAppError — the application routes on
	// StatusCode(): 429 tries another provider, 401/403 marks a provider dead,
	// 413 compacts rather than falls back. Message is frontend-safe;
	// upstream detail stays in Cause.
	Send(ctx context.Context, conv *aggregate.Conversation, prov *aggregate.Provider, model entity.Model, onEvent event.TurnEventHandler) error
}

// CompactionSanitizer is the optional provider capability that owns
// prefix-bound replay artifacts. When compaction replaces the conversation
// prefix, signatures recorded against the old prefix cannot be replayed;
// adapters that emit such artifacts implement this method to return a tail
// with those artifacts removed. The input and its messages are not mutated;
// order and length are preserved so the caller can persist the rewritten
// tail alongside the checkpoint.
//
// Adapters whose replay artifacts remain valid across a prefix change do
// not implement this capability.
type CompactionSanitizer interface {
	SanitizeCompactedTail(tail []entity.Message) []entity.Message
}
