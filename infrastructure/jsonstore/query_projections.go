package jsonstore

import (
	"sort"

	"nusashell/domain"
)

// ListMeta returns one metadata copy per conversation, sorted by UpdatedAt
// descending like List, but without deep-cloning transcripts. Messages is a
// role-only skeleton of the same length: it keeps len(), HasUserMessage, and
// HasDurableAnchor truthful (ConvDTO's MessageCount and the durable-anchor
// check both inspect messages) while carrying no content, reasoning, or tool
// payloads. The copies are private — mutating one cannot touch the store.
func (s *Store) ListMeta() []*domain.Conversation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Conversation, 0, len(s.conversations))
	for _, c := range s.conversations {
		out = append(out, metaCopy(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// metaCopy shallow-copies the conversation scalars and clones the small
// non-transcript slices (PendingAnnouncements, LastAnnouncedRecords) so a
// caller mutating them cannot write into the stored arrays. The transcript
// becomes a role-only skeleton — everything ConvDTO (MessageCount),
// HiddenFromRoomList, and HasDurableAnchor need, at a fraction of a deep
// clone. Callers must not rely on content-bearing helpers (DefaultTitle,
// EstimateTokens, message bodies): those see empty content by design.
func metaCopy(c *domain.Conversation) *domain.Conversation {
	out := *c
	out.PendingAnnouncements = append([]domain.PendingAnnouncement(nil), c.PendingAnnouncements...)
	out.LastAnnouncedRecords = append(domain.AnnouncedRecords(nil), c.LastAnnouncedRecords...)
	msgs := make([]domain.Message, len(c.Messages))
	for i := range c.Messages {
		msgs[i].Role = c.Messages[i].Role
	}
	out.Messages = msgs
	return &out
}

// ListUsage returns one domain.UsageProjection per assistant message that
// reported token usage, across all conversations, without deep-cloning any
// transcript. Rows are value copies — the projection type lives in domain so
// application ports can name it.
func (s *Store) ListUsage() []domain.UsageProjection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.UsageProjection
	for _, c := range s.conversations {
		out = append(out, c.UsageRows()...)
	}
	return out
}
