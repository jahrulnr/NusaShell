package domain

// LastAssistantText returns the visible text content of the assistant message
// with the given ID. Used by the auto-continue policy to detect whether the
// turn ended with a question (which means the agent is waiting for the user).
func LastAssistantText(c *Conversation, messageID string) string {
	for i := range c.Messages {
		if c.Messages[i].ID == messageID && c.Messages[i].Role == RoleAssistant {
			return c.Messages[i].Content
		}
	}
	return ""
}
