package domain

// FilterHydrationDomainMessages strips hydration checkpoint messages (pure
// hydration tool calls, with optional synthetic reasoning) from a Message slice.
// Used by compaction to exclude synthetic runtime snapshots from summaries.
func FilterHydrationDomainMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if IsHydrationMessage(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}
