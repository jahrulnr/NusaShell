package application

import "strings"

// AgentKind identifies the agent personalities that see different tool
// sets. The toolbox holds every tool definition; ToolFactory is the single
// policy table for which agent sees which tools, replacing the scattered
// per-site filters (review whitelist, workspace gating, headless ACP
// removal).
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
	// AgentReview is the unified background learning agent. It receives the
	// completed conversation evidence and can inspect, research, and curate
	// durable memory and agent-owned skills. The name is retained for the
	// learning.review wire surface and persisted history compatibility.
	AgentReview AgentKind = "review"
)

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
func (f *ToolFactory) Get(kind AgentKind, workspace string) []ToolDef {
	if kind == AgentCompaction {
		// The compaction agent advertises exactly one tool and never
		// touches the toolbox or dispatchers, so it works on a zero
		// factory.
		return []ToolDef{compactionSummaryToolDef}
	}
	if f == nil || f.Toolbox == nil {
		return nil
	}
	switch kind {
	case AgentReview:
		return f.reviewTools(workspace)
	case AgentAutomation:
		return filterACPToolDefs(f.baseTools(workspace))
	case AgentDelegate:
		return filterDelegateToolDefs(filterACPToolDefs(f.baseTools(workspace)))
	default:
		return f.baseTools(workspace)
	}
}

// filterDelegateToolDefs removes the delegate tool itself so delegated
// agents cannot spawn further delegates.
func filterDelegateToolDefs(defs []ToolDef) []ToolDef {
	out := make([]ToolDef, 0, len(defs))
	for _, d := range defs {
		if d.Name == "delegate" {
			continue
		}
		out = append(out, d)
	}
	return out
}

// baseTools assembles the shared toolbox + optional compatibility dispatcher
// list as ToolDefs. The real Toolbox already owns the dispatcher roots; the
// second source remains supported for partial/custom toolboxes. Names are
// deduplicated so a root is never sent to a provider twice.
func (f *ToolFactory) baseTools(workspace string) []ToolDef {
	tools := f.Toolbox()
	if f.Dispatchers != nil {
		tools = append(tools, f.Dispatchers(workspace)...)
	}
	workspaceSet := strings.TrimSpace(workspace) != ""
	seen := make(map[string]bool, len(tools))
	out := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		if t.Name == "memory_project" && !workspaceSet {
			continue
		}
		if seen[t.Name] {
			continue
		}
		seen[t.Name] = true
		out = append(out, ToolDef(t))
	}
	return out
}

// reviewTools is the unified AgentReview policy: the local transcript and
// model tools plus read-only evidence/research tools and the memory/skill
// dispatcher roots. The runtime gate decides which operations may mutate.
func (f *ToolFactory) reviewTools(workspace string) []ToolDef {
	out := []ToolDef{reviewTranscriptToolDef, modelOverrideToolDef}
	for _, t := range f.baseTools(workspace) {
		if backgroundLearningToolWhitelist()[t.Name] && t.Name != reviewTranscriptToolName {
			out = append(out, t)
		}
	}
	return out
}

// toolFactoryFor wires the default factory from an App's toolbox and the
// dispatcher families. Bare test Apps without a toolbox get a factory that
// still serves AgentCompaction (which needs no toolbox) and nil otherwise.
func toolFactoryFor(a *App) *ToolFactory {
	var toolbox func() []ToolInfo
	if a != nil && a.Toolbox != nil {
		toolbox = a.Toolbox.ListTools
	}
	return &ToolFactory{Toolbox: toolbox, Dispatchers: FilterDispatcherToolInfos}
}

// turnToolDefs assembles the tool list for a turn: the conversation agent
// in interactive rooms, the automation agent (no ACP tools) for headless
// runs, or the run's explicit ToolKind (internal delegates).
func (a *App) turnToolDefs(run *TurnRun) []ToolDef {
	if a == nil || a.Toolbox == nil {
		return nil
	}
	kind := AgentConversation
	if run.Headless {
		kind = AgentAutomation
	}
	if run.ToolKind != "" {
		kind = run.ToolKind
	}
	return toolFactoryFor(a).Get(kind, run.Workspace)
}
