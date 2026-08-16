package application

import (
	"strings"

	"nusashell/domain"
	"nusashell/resources"
)

// systemPrompt is loaded once from resources/agent/prompts/system.md at
// init time. Edit the markdown file directly — no code changes needed.
var systemPrompt = resources.Prompt("system")

// toolsPrompt is the static tool/context protocol block, loaded from
// resources/agent/prompts/tools.md. Appended to the system prompt on
// every request (cache-stable prefix).
var toolsPrompt = resources.Prompt("tools")

// continuePrompt is the steering prompt injected at the start of each
// auto-continue turn. Loaded from resources/agent/prompts/continue.md.
var continuePrompt = resources.Prompt("continue")

// buildSystemPrompt composes the agent identity + tool protocol with
// compaction summaries and any system-level skill messages stored in the
// conversation. The system.md + tools.md prefix is cache-stable across
// turns; only the tail (system messages, workspace) varies.
func buildSystemPrompt(c *domain.Conversation) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	if toolsPrompt != "" {
		sb.WriteString("\n\n")
		sb.WriteString(toolsPrompt)
	}
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
