package application

import (
	"strings"

	"nusashell/domain"
	"nusashell/resources"
)

// systemPrompt is loaded once from resources/agent/prompts/system.md at init
// time. The markdown owns identity, tool/context protocol, operating rules,
// and intent/evidence routing as one merged document.
var systemPrompt = resources.Prompt("system")

// continuePrompt is the steering prompt injected at the start of each
// auto-continue turn. Loaded from resources/agent/prompts/continue.md.
var continuePrompt = resources.Prompt("continue")

// buildSystemPrompt composes the agent identity + tool protocol (single
// system.md) with compaction summaries and any system-level skill messages
// stored in the conversation. The system.md prefix is cache-stable across
// turns; the user prompt (if set) extends that prefix — changing it
// breaks the prompt cache for all subsequent turns until a new cache
// shard stabilizes. Only the tail (system messages, workspace) varies
// per conversation/turn.
func buildSystemPrompt(c *domain.Conversation, userPrompt string) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	if up := strings.TrimSpace(userPrompt); up != "" {
		sb.WriteString("\n\n<user_instructions>\n")
		sb.WriteString(up)
		sb.WriteString("\n</user_instructions>")
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

var subagentDelegationPrompt = resources.Prompt("subagent-delegation")

func (a *App) acpDelegationPrompt() string {
	agents := a.enabledAcpAgents()
	if len(agents) == 0 || strings.TrimSpace(subagentDelegationPrompt) == "" {
		return ""
	}
	list, def := availableAcpSummary(agents)
	p := strings.ReplaceAll(subagentDelegationPrompt, "{{available_subagents}}", list)
	return strings.ReplaceAll(p, "{{default_subagent}}", def)
}
