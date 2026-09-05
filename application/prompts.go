package application

import (
	"strings"

	"nusashell/domain"
	"nusashell/resources"
)

// systemPrompt is loaded once from resources/agent/prompts/system.md at init
// time. The markdown owns identity, tool/context protocol, operating rules,
// and intent/evidence routing as one merged document.
var systemPrompt = resources.SystemPrompt()

// automationPrompt is the system prompt for headless Automation workflow
// agent steps. It is loaded once from
// resources/agent/prompts/automation-agent.md at init time. Unlike the
// interactive system prompt, it frames the agent as a focused step executor
// with no user watching the stream, no ACP subagent tools, and concise
// output captured as the step result.
var automationPrompt = resources.Prompt("automation-agent")

// delegatePrompt is the system prompt for the internal delegate agent. It is
// intentionally separate from both the interactive and automation prompts:
// delegate output is consumed by a parent agent, and the delegate must finish
// the assigned work before returning its terminal assistant message.
var delegatePrompt = resources.Prompt("delegate-agent")

// continuePrompt is the auto-continue guidance delivered as the output of
// the synthetic `announcement` tool call injected at the start of each
// auto-continue turn. Loaded from resources/agent/prompts/continue.md.
var continuePrompt = resources.Prompt("continue")

// compactionPrompt is the system prompt for the compaction summarization
// call. Loaded from resources/agent/prompts/compaction.md. Tells the model
// to call the summary() tool with the handoff checkpoint text.
var compactionPrompt = resources.Prompt("compaction")

// learnerPrompt is the background learner system prompt. The model keeps the
// full conversation toolbox and commits catalog records with learn(), the
// same way compaction commits a handoff with summary().
var learnerPrompt = resources.LearnerPrompt()

// compactionHandoffUserPrompt is the last user message on a compaction
// Complete request. System-prompt instructions are not enough: if the
// transcript ends on assistant/tool, reasoning models continue the agent
// turn. This closer makes the current instruction a user turn.
var compactionHandoffUserPrompt = resources.UserPrompt("compaction")

// buildSystemPrompt composes the agent identity + tool protocol (single
// system.md) with any system-level skill messages stored in the conversation.
// The system.md prefix is cache-stable across turns; the user prompt (if set)
// extends that prefix — changing it breaks the prompt cache for all subsequent
// turns until a new cache shard stabilizes. Only the tail (system messages)
// varies per conversation/turn.
//
// The active workspace is NOT appended here — it travels in the
// runtime_context hydration slot (see HydrationBuilder.readRuntimeContext),
// which is re-injected whenever the workspace changes (the pick-workspace
// handler strips the stale checkpoint) or after compaction.
// Duplicating it in the system prompt would break cache stability on
// workspace switch for no benefit.
//
// Compaction summaries carry role=user (not system) so they appear in the
// provider request's messages array as the first live message after
// compaction — see domain.CompactionSummaryPrefix.
func buildSystemPrompt(c *domain.Conversation, userPrompt string) string {
	return buildSystemPromptForRun(nil, c, userPrompt)
}

// buildSystemPromptForRun composes the system prompt for a specific turn run.
// Headless Automation pipeline steps and internal delegates use their own
// prompts instead of the interactive system prompt. The interactive path is
// unchanged when run is nil (e.g. tests).
func buildSystemPromptForRun(run *TurnRun, c *domain.Conversation, userPrompt string) string {
	base := systemPrompt
	if run != nil && run.Headless {
		switch run.ToolKind {
		case AgentAutomation:
			base = automationPrompt
		case AgentDelegate:
			base = delegatePrompt
		case AgentLearner, AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator:
			base = learnerPrompt
		}
	}
	var sb strings.Builder
	sb.WriteString(base)
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
	list, def := domain.AvailableAcpSummary(agents)
	p := strings.ReplaceAll(subagentDelegationPrompt, "{{available_subagents}}", list)
	return strings.ReplaceAll(p, "{{default_subagent}}", def)
}
