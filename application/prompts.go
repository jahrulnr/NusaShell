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

// compactionPrompt is the system prompt for the compaction summarization
// call. Loaded from resources/agent/prompts/compaction.md. Tells the model
// to call the summary() tool with the handoff checkpoint text.
var compactionPrompt = resources.Prompt("compaction")

// buildSystemPrompt composes the agent identity + tool protocol (single
// system.md) with any system-level skill messages stored in the conversation.
// The system.md prefix is cache-stable across turns; the user prompt (if set)
// extends that prefix — changing it breaks the prompt cache for all subsequent
// turns until a new cache shard stabilizes. Only the tail (system messages,
// workspace) varies per conversation/turn.
//
// Compaction summaries carry role=user (not system) so they appear in the
// provider request's messages array — see domain.CompactionSummaryPrefix.
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

// AcpDelegationDescription renders the subagent delegation guidance with
// the enabled agent list filled in. It is attached to the `subagent` tool
// description (never the system prompt) so the system prefix stays
// cache-stable and runtime config lives with the tool it describes.
// Returns "" when no agents are enabled or the template is empty.
func AcpDelegationDescription(agents []*domain.AcpAgent) string {
	if len(agents) == 0 || strings.TrimSpace(subagentDelegationPrompt) == "" {
		return ""
	}
	list, def := availableAcpSummary(agents)
	p := strings.ReplaceAll(subagentDelegationPrompt, "{{available_subagents}}", list)
	return strings.ReplaceAll(p, "{{default_subagent}}", def)
}
