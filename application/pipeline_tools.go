package application

import (
	"context"
	"fmt"
	"nusashell/domain"
)

// ACPToolNames is the spawn-only ACP surface. Interactive conversations may
// advertise these when an ACP agent is enabled. Pipeline agent steps must not.
func ACPToolNames() []string {
	return []string{"subagent", "subagent_steer", "subagent_stop", "subagent_wait"}
}

// IsACPTool reports whether name is an ACP subagent tool.
func IsACPTool(name string) bool {
	switch name {
	case "subagent", "subagent_steer", "subagent_stop", "subagent_wait":
		return true
	default:
		return false
	}
}

// FilteredToolbox wraps a ToolExecutor and hides matching tools from both
// ListTools and Execute. Used so pipeline agent steps cannot see or call
// tools that require an interactive approval UI.
type FilteredToolbox struct {
	Inner ToolExecutor
	Hide  func(name string) bool
}

// FilterACPTools hides ACP subagent tools from inner.
func FilterACPTools(inner ToolExecutor) *FilteredToolbox {
	return &FilteredToolbox{Inner: inner, Hide: IsACPTool}
}

func (f *FilteredToolbox) ListTools() []ToolInfo {
	if f == nil || f.Inner == nil {
		return nil
	}
	all := f.Inner.ListTools()
	out := make([]ToolInfo, 0, len(all))
	for _, t := range all {
		if f.Hide != nil && f.Hide(t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (f *FilteredToolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	if f != nil && f.Hide != nil && f.Hide(name) {
		return "", fmt.Errorf("tool %q is not available to pipeline agent steps", name)
	}
	if f == nil || f.Inner == nil {
		return "", fmt.Errorf("toolbox is not configured")
	}
	return f.Inner.Execute(ctx, name, argsJSON)
}

// PipelineAgentRunner is the AgentStepRunner for unattended workflow agent
// steps. It never advertises ACP tools: those permission prompts are
// fail-closed and would stall FireDue with no operator at the dock.
type PipelineAgentRunner struct {
	Tools ToolExecutor
	Turns HeadlessTurnRunner
}

// NewPipelineAgentRunner wraps inner so ACP tools are invisible and
// unexecutable. Interactive App.Toolbox is left unchanged. turns is the
// HeadlessTurnRunner that executes the actual agent turn; nil leaves
// RunAgentStep returning "not configured" (stub behavior for tests that
// only check ACP filtering).
func NewPipelineAgentRunner(inner ToolExecutor, turns HeadlessTurnRunner) *PipelineAgentRunner {
	return &PipelineAgentRunner{Tools: FilterACPTools(inner), Turns: turns}
}

func (r *PipelineAgentRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error) {
	if r == nil || r.Tools == nil {
		return nil, "", fmt.Errorf("agent steps are not configured")
	}
	for _, t := range r.Tools.ListTools() {
		if IsACPTool(t.Name) {
			return nil, "", fmt.Errorf("internal: ACP tool %q must not be visible to pipeline agents", t.Name)
		}
	}
	if r.Turns == nil {
		return nil, "", fmt.Errorf("agent steps are not configured")
	}
	return r.Turns.RunHeadlessTurn(ctx, prompt, model, trust, schema)
}

// filterACPToolDefs removes ACP subagent tools from a ToolDef slice. Used by
// headless turns to ensure pipeline agent steps never see subagent tools.
func filterACPToolDefs(defs []ToolDef) []ToolDef {
	out := make([]ToolDef, 0, len(defs))
	for _, d := range defs {
		if !IsACPTool(d.Name) {
			out = append(out, d)
		}
	}
	return out
}
