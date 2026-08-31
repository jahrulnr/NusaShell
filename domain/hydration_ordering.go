package domain

import (
	"slices"
	"strings"
)

// Hydration transcript ordering rules.
//
// OpenAI Chat Completions and Anthropic Messages require a user message
// before any assistant+tool hydration turn: the provider payload must be
// system → user → hydration. These pure functions enforce and repair that
// invariant on a domain.Message slice without touching I/O.

// HydrationInsertIndex returns the index immediately after the first user
// message, or -1 when no user exists. The hydration checkpoint is parked
// there so it stays put across later follow-up users and steers.
func HydrationInsertIndex(msgs []Message) int {
	for i := range msgs {
		if msgs[i].Role == RoleUser {
			return i + 1
		}
	}
	return -1
}

// HydrationPrecedesFirstUser reports a protocol-invalid checkpoint: the
// synthetic assistant+tool turn sits before any user message, so the
// provider payload would be system → assistant → tool → user.
func HydrationPrecedesFirstUser(msgs []Message) bool {
	for _, m := range msgs {
		if m.Role == RoleUser {
			return false
		}
		if IsHydrationMessage(m) {
			return true
		}
	}
	return false
}

// RelocateHydrationAfterFirstUser moves a leading hydration checkpoint to
// immediately after the first user. Existing call IDs are preserved so the
// prompt-cache prefix is not rebuilt. If no user exists the orphan
// checkpoint is dropped.
func RelocateHydrationAfterFirstUser(msgs []Message) []Message {
	if !HydrationPrecedesFirstUser(msgs) {
		return msgs
	}
	hyd := make([]Message, 0, 1)
	rest := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if IsHydrationMessage(m) {
			hyd = append(hyd, m)
			continue
		}
		rest = append(rest, m)
	}
	idx := HydrationInsertIndex(rest)
	if idx < 0 {
		return rest
	}
	return slices.Insert(rest, idx, hyd...)
}

// IsFreshRoom reports whether the conversation is on its first user turn: at
// most one user message. A fresh room has exactly the opening user;
// follow-up turns and post-steer turns have two or more users.
// Post-compaction turns are excluded by the HasHydration guard in the
// caller (the checkpoint is rebuilt by the compaction path), so a
// handover-only transcript with no checkpoint still counts as fresh only
// when it has no prior user — which compaction never produces (the handover
// is a user, and the checkpoint is built in the same Save).
func IsFreshRoom(c *Conversation) bool {
	if c == nil {
		return true
	}
	userCount := 0
	for _, m := range c.Messages {
		if m.Role == RoleUser {
			userCount++
		}
	}
	return userCount <= 1
}

// IsHydrationMessage returns true when a message is a pure hydration
// checkpoint — all tool calls have the "hydrate-" prefix and there is no
// visible content or reasoning. These messages are hidden from the UI and
// excluded from compaction summaries.
func IsHydrationMessage(m Message) bool {
	if len(m.ToolCalls) == 0 || strings.TrimSpace(m.Content) != "" || strings.TrimSpace(m.Reasoning) != "" {
		return false
	}
	for _, tc := range m.ToolCalls {
		if !IsHydrationCallID(tc.ID) {
			return false
		}
	}
	return true
}

// FilterHydrationToolCalls strips hydration tool calls from a message that
// has both real and hydration tool calls (mixed). Returns the message
// unchanged if it has no hydration tool calls. Steps are filtered in sync
// so a reloaded conversation preserves the completed terminal output and
// status shown during streaming.
func FilterHydrationToolCalls(m Message) Message {
	if len(m.ToolCalls) == 0 {
		return m
	}
	hasHydration := false
	for _, tc := range m.ToolCalls {
		if IsHydrationCallID(tc.ID) {
			hasHydration = true
			break
		}
	}
	if !hasHydration {
		return m
	}
	filtered := make([]ToolCall, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		if !IsHydrationCallID(tc.ID) {
			filtered = append(filtered, tc)
		}
	}
	m.ToolCalls = filtered
	for i := range m.Steps {
		if len(m.Steps[i].ToolCalls) == 0 {
			continue
		}
		stepFiltered := make([]ToolCall, 0, len(m.Steps[i].ToolCalls))
		for _, tc := range m.Steps[i].ToolCalls {
			if !IsHydrationCallID(tc.ID) {
				stepFiltered = append(stepFiltered, tc)
			}
		}
		m.Steps[i].ToolCalls = stepFiltered
	}
	return m
}
