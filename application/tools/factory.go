package tools

import "strings"

// AgentKind identifies the agent personalities that see different tool
// sets. The toolbox holds every tool definition; ToolFactory is the single
// policy table for which agent sees which tools, replacing the scattered
// per-site filters (workspace gating, headless ACP removal).
type AgentKind string

const (
	// AgentConversation is the interactive room agent: the full toolbox
	// plus dispatcher families, with memory_project gated on the
	// conversation workspace.
	AgentConversation AgentKind = "conversation"
	// AgentAutomation is the headless pipeline agent step: the full
	// toolbox minus ACP subagent tools (permission prompts must never
	// stall a headless run).
	AgentAutomation AgentKind = "automation"
	// AgentCompaction is the context-compaction summarizer: exactly one
	// local tool, summary(), forced via ToolChoice. It never touches the
	// toolbox or dispatchers.
	AgentCompaction AgentKind = "compaction"
	// AgentDelegate is the internal delegation agent (the `delegate`
	// tool): a headless run of the conversation rules in a hidden
	// pipeline room, with ACP tools AND the delegate tool itself removed
	// so delegated agents cannot recurse.
	AgentDelegate AgentKind = "delegate"
	// AgentLearner is the unified background learning agent (memory
	// consolidate + optional skill evaluate/evolve in one spawn). It
	// receives the conversation toolbox minus project memory, ACP/
	// delegate, and MCP families, plus learn() for typed catalog commits.
	// Profile writes still use file_*. Cross-room inspection uses
	// conversation(op=list|search|read|info).
	AgentLearner AgentKind = "learner"
	// AgentMemoryConsolidator is a legacy alias for AgentLearner.
	AgentMemoryConsolidator AgentKind = "memory-consolidator"
	// AgentSkillEvolver is a legacy alias for AgentLearner.
	AgentSkillEvolver AgentKind = "skill-evolver"
	// AgentSkillEvaluator is a legacy alias for AgentLearner.
	AgentSkillEvaluator AgentKind = "skill-evaluator"
)

// CompactionSummaryToolName is the single tool advertised to the compaction
// model. The summary lives in the tool-call arguments, not assistant text.
const CompactionSummaryToolName = "summary"

// CompactionSummaryTool is the tool definition for summary().
var CompactionSummaryTool = ToolInfo{
	Name:        CompactionSummaryToolName,
	Description: "Submit the conversation handoff summary. Call this exactly once with the complete checkpoint text.",
	InputSchema: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The complete handoff checkpoint summary for the next LLM.",
			},
		},
		"required": []string{"text"},
	},
}

// ToolFactory builds the advertised tool list per agent kind. The factory
// is stateless: it holds the two tool sources and derives each agent's
// list on demand.
type ToolFactory struct {
	// Toolbox lists every tool definition the store can execute.
	Toolbox func() []ToolInfo
	// Dispatchers lists the dispatcher-family definitions (skill, memory, docs,
	// memory_project, automation, automation_schedule). memory_project is
	// workspace-gated by the implementation.
	Dispatchers func(workspace string) []ToolInfo
}

// Get returns the tool definitions advertised to the agent kind.
func (f *ToolFactory) Get(kind AgentKind, workspace string) []ToolInfo {
	if kind == AgentCompaction {
		// The compaction agent advertises exactly one tool and never
		// touches the toolbox or dispatchers, so it works on a zero
		// factory.
		return []ToolInfo{CompactionSummaryTool}
	}
	if f == nil || f.Toolbox == nil {
		return nil
	}
	switch kind {
	case AgentLearner, AgentMemoryConsolidator, AgentSkillEvolver, AgentSkillEvaluator:
		return WithLearnerResultTool(filterLearnerToolInfos(f.baseTools(workspace)))
	case AgentAutomation:
		return filterHeadlessToolInfos(f.baseTools(workspace))
	case AgentDelegate:
		return filterHeadlessToolInfos(f.baseTools(workspace))
	default:
		return f.baseTools(workspace)
	}
}

// baseTools assembles the shared toolbox + optional compatibility dispatcher
// list. The real Toolbox already owns the dispatcher roots; the second
// source remains supported for partial/custom toolboxes. Names are
// deduplicated so a root is never sent to a provider twice.
func (f *ToolFactory) baseTools(workspace string) []ToolInfo {
	infos := f.Toolbox()
	if f.Dispatchers != nil {
		infos = append(infos, f.Dispatchers(workspace)...)
	}
	workspaceSet := strings.TrimSpace(workspace) != ""
	seen := make(map[string]bool, len(infos))
	out := make([]ToolInfo, 0, len(infos))
	for _, t := range infos {
		if t.Name == "memory_project" && !workspaceSet {
			continue
		}
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, t)
	}
	return out
}
