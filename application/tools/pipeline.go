package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// IsACPTool reports whether name is a subagent-family tool. The legacy
// `delegate` name is the same tool targeting the internal delegate.
func IsACPTool(name string) bool {
	switch name {
	case "subagent", "subagent_steer", "subagent_stop", "subagent_wait", "delegate":
		return true
	default:
		return false
	}
}

// IsPipelineBannedTool reports tool names that must never reach unattended
// headless turns (pipeline agent steps and internal delegates): ACP subagent
// tools surface interactive permission prompts, and ask_question blocks until
// a human answers in the Agent UI. No operator is at the dock for a headless
// turn, so exposing any of them stalls the run until its context is cancelled.
func IsPipelineBannedTool(name string) bool {
	return IsACPTool(name) || name == "ask_question" || IsInternalToolName(name)
}

// IsLearnerBannedTool reports tools the learner must neither advertise nor
// execute: project memory, ACP/delegate, host-internal tools, and the MCP
// family (including discovery companions that only serve MCP).
func IsLearnerBannedTool(name string) bool {
	if IsInternalToolName(name) {
		return true
	}
	switch name {
	case "memory_project", "delegate", "tool_list", "tool_schema", "contract_read":
		return true
	}
	if IsACPTool(name) {
		return true
	}
	return strings.HasPrefix(name, "mcp_")
}

// ExecAsyncAllowed reports whether the agent kind may use exec's detached
// process surface (background spawn plus the status/wait/kill/list ops).
// Only the interactive conversation agent owns background processes;
// headless kinds (pipeline steps, internal delegates, learners, compaction)
// get the sync-only exec schema and are also rejected here so a stale or
// hallucinated call cannot bypass the advertised contract. Interactive runs
// carry the zero-value kind, so "" is allowed.
func ExecAsyncAllowed(kind AgentKind) bool {
	return kind == "" || kind == AgentConversation
}

// IsAsyncExecCall reports whether an exec call touches the detached process
// surface: background=true on run, or any of the status/wait/kill/list ops.
// Unknown/malformed args are not async — the sync path fails them loud on
// its own validation.
func IsAsyncExecCall(name string, argsJSON []byte) bool {
	if name != "exec" {
		return false
	}
	var args struct {
		Background bool `json:"background"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return false
	}
	if args.Background {
		return true
	}
	switch OpArg(argsJSON) {
	case "status", "wait", "kill", "list":
		return true
	}
	return false
}

// syncOnlyExecTool rewrites the exec definition for agent kinds that may not
// own detached processes: the async properties (op, background, id) are
// stripped and the description advertises only the foreground contract.
func syncOnlyExecTool(d ToolInfo) ToolInfo {
	schema := make(map[string]any, len(d.InputSchema))
	for k, v := range d.InputSchema {
		schema[k] = v
	}
	if p, ok := d.InputSchema["properties"].(map[string]any); ok {
		cp := make(map[string]any, len(p))
		for k, v := range p {
			cp[k] = v
		}
		delete(cp, "op")
		delete(cp, "background")
		delete(cp, "id")
		schema["properties"] = cp
	}
	schema["required"] = []string{"command"}
	d.InputSchema = schema
	d.Description = syncExecDescription
	return d
}

// syncExecDescription mirrors the foreground-only exec contract for agent
// kinds without the detached process surface.
const syncExecDescription = `Run a shell command as a child process and return combined stdout/stderr. Default shell: POSIX sh on Unix/macOS; on Windows auto-resolves Git Bash then PowerShell (cmd only via shell="cmd"). Optional shell kind: bash, powershell, pwsh, cmd, wsl. No absolute wall-clock limit: a running command that keeps producing output keeps running. Silence longer than idle_timeout_ms (default 180000) cancels the run as failed. Optional timeout_ms adds an explicit hard cap. Long-lived processes are killed together with their children. On Windows, select shells via the shell parameter rather than invoking cmd.exe or powershell.exe inside a bash command line — MSYS path conversion mangles drive-letter paths such as Z:/x. Combined output is streamed live. In-band stdout/stderr is capped at 20000 characters as a 50/50 head+tail sample with "... (output truncated) ..." in the middle. When the full log is larger, overflow_path is an absolute file under the platform temp dir (nusashell/); file_read it from offset 0 for the complete stdout/stderr.`

// IsLearnerSkillMutation reports the skill operations that a learner must not
// execute directly. The periodic learner is memory-only; skill changes belong
// to an explicit skill-authoring workflow.
func IsLearnerSkillMutation(name string, argsJSON []byte) bool {
	if name != "skill" {
		return false
	}
	switch OpArg(argsJSON) {
	case "save", "delete":
		return true
	default:
		return false
	}
}

// filterLearnerToolInfos removes tools banned from learner agent kinds.
// exec survives but loses its detached-process surface: the learner runs
// unattended and must not orphan background processes.
func filterLearnerToolInfos(defs []ToolInfo) []ToolInfo {
	out := make([]ToolInfo, 0, len(defs))
	for _, d := range defs {
		if IsLearnerBannedTool(d.Name) {
			continue
		}
		switch d.Name {
		case "skill":
			d = learnerReadOnlySkillTool()
		case "exec":
			d = syncOnlyExecTool(d)
		}
		out = append(out, d)
	}
	return out
}

// learnerReadOnlySkillTool keeps skill discovery available while removing
// write-shaped fields from the learner's provider-facing schema. The runtime
// also rejects save/delete calls so a stale or hallucinated call cannot bypass
// this advertised contract.
func learnerReadOnlySkillTool() ToolInfo {
	return ToolInfo{
		Name:        "skill",
		Description: `Read the skill catalog only; "op" selects: list {limit?,status?} or search {query,limit?,status?}. The catalog includes read-only workspace/global source packages with builtin > workspace > global collision priority. Read the selected SKILL.md with file_read before applying it. Skill writes are committed by the learner runtime after the final learn() result, not by this dispatcher.`,
		InputSchema: objSchema(
			pEnum("op", "Read-only operation", "list", "search"),
			pStr("query", "Search query (op=search)"),
			pInt("limit", "Max results (list default 100, search default 50)"),
			pStr("status", "Optional status filter (list/search). Empty = routable trusted|validated only."),
		),
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

// FilterPipelineTools hides everything unattended turns must not call
// (ACP tools and human-in-the-loop barrier tools such as ask_question) from
// inner. Used by pipeline agent steps and internal delegates.
func FilterPipelineTools(inner ToolExecutor) *FilteredToolbox {
	return &FilteredToolbox{Inner: inner, Hide: IsPipelineBannedTool}
}

// FilterInternalToolsExec hides host-internal tool names from ListTools and
// Execute. Use when wrapping a toolbox that may expose MCP plugin tools by
// bare name (or any executor that can surface internal_* / admin.* tools).
func FilterInternalToolsExec(inner ToolExecutor) *FilteredToolbox {
	return &FilteredToolbox{Inner: inner, Hide: IsInternalToolName}
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

// ExecuteStreamed forwards streaming tool execution to the inner toolbox
// when it supports it (optional capability, mirroring ToolExecutor without
// widening the interface); otherwise it falls back to the plain Execute path.
func (f *FilteredToolbox) ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error) {
	if f != nil && f.Hide != nil && f.Hide(name) {
		return "", fmt.Errorf("tool %q is not available to pipeline agent steps", name)
	}
	if f == nil || f.Inner == nil {
		return "", fmt.Errorf("toolbox is not configured")
	}
	if s, ok := f.Inner.(interface {
		ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error)
	}); ok {
		return s.ExecuteStreamed(ctx, name, argsJSON, onChunk)
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
	return &PipelineAgentRunner{Tools: FilterPipelineTools(inner), Turns: turns}
}

func (r *PipelineAgentRunner) RunAgentStep(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, conversationID string, onUpdate func(conversationID string)) (map[string]any, string, error) {
	if r == nil || r.Tools == nil {
		return nil, "", fmt.Errorf("agent steps are not configured")
	}
	for _, t := range r.Tools.ListTools() {
		if IsPipelineBannedTool(t.Name) {
			return nil, "", fmt.Errorf("internal: pipeline-banned tool %q must not be visible to pipeline agents", t.Name)
		}
	}
	if r.Turns == nil {
		return nil, "", fmt.Errorf("agent steps are not configured")
	}
	return r.Turns.RunHeadlessTurn(ctx, prompt, model, trust, schema, conversationID, onUpdate)
}

// filterHeadlessToolInfos removes pipeline-banned tools (ACP subagent tools
// and human-in-the-loop barrier tools such as ask_question) from a ToolInfo
// slice and strips exec's detached-process surface: unattended agents may
// run commands but never own background processes. Used by headless turns
// so unattended agents never see tools that require an interactive operator
// at the dock.
func filterHeadlessToolInfos(defs []ToolInfo) []ToolInfo {
	out := make([]ToolInfo, 0, len(defs))
	for _, d := range defs {
		if IsPipelineBannedTool(d.Name) {
			continue
		}
		if d.Name == "exec" {
			d = syncOnlyExecTool(d)
		}
		out = append(out, d)
	}
	return out
}
