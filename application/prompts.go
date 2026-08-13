package application

import (
	"strings"

	"nusashell/domain"
)

// buildSystemPrompt composes the agent identity with compaction summaries and
// any system-level skill messages stored in the conversation.
func buildSystemPrompt(c *domain.Conversation) string {
	var sb strings.Builder
	sb.WriteString(`You are NusaShell Light, a personal AI agent running locally on the user's machine. You are helpful, precise and direct.

You have tools available. Use them when they clearly help: skills for reusable procedures (skill_list, skill_run), memory for facts worth remembering (memory_save, memory_search), docs for product documentation (docs_search, docs_read), and mcp_call tools for anything exposed by the user's MCP servers (named mcp__<server>__<tool>).

Rules:
- Answer in the user's language.
- When you use a tool, continue naturally after seeing its result.
- Never invent tool outputs; rely on what tools return.
- If a tool fails, report the error and suggest a fix.`)
	for _, m := range c.Messages {
		if m.Role == domain.RoleSystem && strings.TrimSpace(m.Content) != "" {
			sb.WriteString("\n\n")
			sb.WriteString(m.Content)
		}
	}
	if c.Workspace != "" {
		sb.WriteString("\n\nThe active workspace for this conversation is: ")
		sb.WriteString(c.Workspace)
		sb.WriteString(". Treat it as the working directory when using workspace-aware tools.")
	}
	return sb.String()
}
