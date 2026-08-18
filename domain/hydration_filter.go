package domain

// FilterHydrationDomainMessages strips hydration checkpoint messages (pure
// hydration tool calls, no content/reasoning) from a Message slice.
// Used by compaction to exclude synthetic runtime snapshots from summaries.
func FilterHydrationDomainMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if len(m.ToolCalls) > 0 && m.Content == "" && m.Reasoning == "" {
			allHydration := true
			for _, tc := range m.ToolCalls {
				if !IsHydrationCallID(tc.ID) {
					allHydration = false
					break
				}
			}
			if allHydration {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}
