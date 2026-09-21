// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*, mcp_* meta-tools). MCP plugin tools are executed via
// mcp_call with a ref (<plugin-id>:<tool>, e.g. nusashell.files:read);
// mcp__<server>__<tool> names are not callable.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"

	"github.com/jahrulnr/searchwire"
)

func ptrBool(v bool) *bool { return &v }

type searchwireSearchFunc func(context.Context, *searchwire.Searcher, string, searchwire.SearchOptions) (*searchwire.Response, error)

type Toolbox struct {
	Skills application.SkillStore
	// RuntimeSkills is the read-only discovery view used by agent skill
	// operations. It adds active-workspace and host-global packages without
	// changing the managed store used by learning and UI persistence.
	RuntimeSkills   application.RuntimeSkillCatalog
	SkillSearcher   application.SkillSearcher // optional; nil = substring fallback
	MemoryRecords   application.MemoryRecordStore
	ProjectMemory   application.ProjectMemoryStore
	Docs            application.DocsSource
	Plugins         application.PluginStore
	PluginInstaller application.PluginInstaller
	Todos           application.ConversationTodoPort
	Conversations   application.ConversationMessenger
	Searcher        *searchwire.Searcher // startup searcher for web_fetch + web_search fallback when settings are unavailable
	Providers       application.ProviderStore
	Settings        application.SettingsStore
	Credentials     application.CredentialStore
	CodexSearch     application.CodexSearchExecutor
	AskQuestions    *application.AskQuestionService
	Automation      *application.Automation
	// SpeechOfflineAvailable flips the generate_speech tool on when the local
	// piper engine is wired (set by the composition root at startup).
	SpeechOfflineAvailable bool
	// Contracts reads plugin usage contracts declared via manifest
	// contract.entry. nil disables contract_read and the gate.
	Contracts ContractSource
	MCP       interface {
		Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
		ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
	}
	Acp interface {
		Subagent(ctx context.Context, argsJSON []byte) (string, error)
		EnabledAcpAgents() []*domain.AcpAgent
	}
	// Delegate spawns internal NusaShell background agents (the
	// `delegate` tool). Optional: nil means the tool is not advertised.
	Delegate interface {
		SpawnDelegate(ctx context.Context, argsJSON []byte) (string, error)
	}
	Steerer interface {
		SteerHeadlessTurn(conversationID, text string) error
	}
	// contractsGate tracks per-conversation contract reads for the gate.
	// Initialized lazily via gate(); safe on the zero value.
	contractsGateOnce sync.Once
	contractsGate     *contractGate
	// execReg tracks detached background exec processes (exec
	// background=true and the status/wait/kill/list ops). Initialized
	// lazily via execRegistry(); terminated by Close at shutdown.
	execRegOnce sync.Once
	execReg     *execRegistry
	// webSearchRR is the round-robin cursor for the web_search provider
	// strategy (Settings → Web Search). Atomic so concurrent tool calls
	// rotate without coordination.
	webSearchRR atomic.Uint64
	// searchwireSearch is an unexported seam for deterministic search tests.
	// Production calls use Searcher.SearchWithOptions directly.
	searchwireSearch searchwireSearchFunc
}

// depMissing is the standard guard error for an optional tool dependency
// that was not wired at startup; msg keeps each site's established wording.
func depMissing(msg string) error {
	return errors.New(msg)
}

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}

func (t *Toolbox) ListTools() []application.ToolInfo {
	registry := t.toolRegistry()
	tools := make([]application.ToolInfo, 0, len(registry))
	for _, e := range registry {
		if e.enabled != nil && !e.enabled(t) {
			continue
		}
		info := e.info
		if e.describe != nil {
			info.Description = e.describe(t)
		}
		tools = append(tools, info)
	}
	// MCP plugin tools are NOT advertised to the agent. The tool list must
	// stay stable for the lifetime of a conversation so the provider prompt
	// cache (OpenAI / Claude) is not invalidated. The agent discovers MCP
	// tools via mcp_list, tool_list, and tool_schema, and
	// executes them only through mcp_call with a ref (<plugin-id>:<tool>) —
	// mcp__<server>__<tool> names are not callable. MCP tools are available
	// to pipeline workflow steps (capability resolution) and the Plugins UI.
	// Native file CRUD + exec built-ins.
	tools = append(tools, fileToolInfos()...)
	tools = append(tools, execToolInfos()...)
	// Dispatcher roots are part of the Toolbox roster as well as the
	// execution surface. ToolFactory applies workspace/agent policy and
	// deduplicates roots when assembling a turn.
	tools = append(tools, application.DispatcherToolInfos()...)
	return tools
}

// executeFamily routes an advertised dispatcher-root call to its op handler.
// This is the only door to the family
// handlers: the root+"_"+op strings below are private routing keys and are
// not reachable as tool names, so retired per-op calls fail loud upstream.
func (t *Toolbox) executeFamily(ctx context.Context, name string, argsJSON []byte) (string, error) {
	op, err := application.DispatchOp(name, argsJSON)
	if err != nil {
		return "", err
	}
	if name == "automation" || name == "automation_schedule" {
		var privateName string
		if name == "automation" {
			privateName = "automation_" + op
			if op == "status" {
				privateName = "automation_run_status"
			}
		} else {
			privateName = "automation_schedule_" + op
		}
		out, handled, err := t.executeAutomation(ctx, privateName, argsJSON)
		if !handled {
			return "", fmt.Errorf("unknown %s op %q", name, op)
		}
		return out, err
	}
	name = name + "_" + op // private routing key; never escapes this method
	switch {
	case strings.HasPrefix(name, "conversation_"):
		return t.executeConversationFamily(ctx, op, argsJSON)
	case strings.HasPrefix(name, "skill_"):
		return t.executeSkillFamily(ctx, op, argsJSON)
	case strings.HasPrefix(name, "memory_project_"):
		return t.executeProjectMemoryFamily(ctx, op, argsJSON)
	case strings.HasPrefix(name, "memory_"):
		return t.executeMemoryFamily(ctx, op, argsJSON)
	case strings.HasPrefix(name, "docs_"):
		return t.executeDocsFamily(ctx, op, argsJSON)
	default:
		return "", fmt.Errorf("unknown %s op %q", name, op)
	}
}

// execRegistry lazily creates the background-process registry; safe on the
// zero-value Toolbox used by tests.
func (t *Toolbox) execRegistry() *execRegistry {
	t.execRegOnce.Do(func() { t.execReg = newExecRegistry() })
	return t.execReg
}

// Close terminates every managed background exec process. The composition
// root defers it so detached children never outlive the app.
func (t *Toolbox) Close() {
	t.execRegistry().close()
}

// ExecuteStreamed runs a tool while forwarding live output chunks to onChunk.
// Only tools that produce streaming output (exec) honor the callback today;
// every other tool executes exactly like Execute and ignores it. Returns the
// final combined output string and error, same contract as Execute.
func (t *Toolbox) ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error) {
	if name == "exec" {
		_, out, err := t.dispatchExec(ctx, name, argsJSON, onChunk)
		return out, err
	}
	return t.Execute(ctx, name, argsJSON)
}

func (t *Toolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	// Native built-ins first: file CRUD and the exec island.
	if name == "exec" {
		_, out, err := t.dispatchExec(ctx, name, argsJSON, nil)
		return out, err
	}
	if strings.HasPrefix(name, "file_") || name == "grep" || name == "find_file" || name == "show" {
		ok, out, err := executeFileToolCtx(ctx, name, argsJSON)
		if !ok {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return out, err
	}
	// Dispatcher families (advertised roots: skill, memory, docs,
	// memory_project, automation, automation_schedule) are routed exclusively through executeFamily: resolving
	// the required `op` and reaching a family handler is impossible without
	// going through it. Retired per-op names fall through to the registry
	// lookup below and end up as the honest "unknown tool" error.
	if application.IsDispatchRoot(name) {
		return t.executeFamily(ctx, name, argsJSON)
	}
	for _, e := range t.toolRegistry() {
		if e.info.Name == name && e.handler != nil {
			return e.handler(ctx, argsJSON)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

// ---- json schema helpers ----

func obj(typ string, properties map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": typ}
	if len(properties) > 0 {
		m["properties"] = properties
	} else if typ == "object" {
		// Some providers (e.g. Bedrock) reject object schemas without a
		// "properties" key. Emit an empty object to stay OpenAI-compatible.
		m["properties"] = map[string]any{}
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// freeObj builds a free-form object schema with a description and an empty
// properties map (Bedrock rejects object schemas without a "properties"
// key). It deliberately sets no additionalProperties key: true would
// recreate the open-object function-calling trap that made weak models emit
// {}, false would block strict-mode adoption later. Empty-argument
// regressions are caught by the mcp_call MISSING_ARGS guard instead.
func freeObj(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc, "properties": map[string]any{}}
}

// requiredSchemaFields returns the field names a tool's input schema marks
// as required, or nil when the schema is absent, invalid, or declares none.
func requiredSchemaFields(schema json.RawMessage) []string {
	var s struct {
		Required []string `json:"required"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Required) == 0 {
		return nil
	}
	return s.Required
}

func props(entries ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(entries); i += 2 {
		name, _ := entries[i].(string)
		out[name] = entries[i+1]
	}
	return out
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func arr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// arrObj builds an array-of-objects JSON schema with the given item
// properties, required fields, and description.
func arrObj(desc string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       obj("object", properties, required...),
	}
}

// strEnum builds a string schema restricted to the given enum values.
func strEnum(desc string, values ...string) map[string]any {
	enums := make([]any, len(values))
	for i, v := range values {
		enums[i] = v
	}
	return map[string]any{"type": "string", "description": desc, "enum": enums}
}

// matchMCPTool was removed: mcp__<server>__<tool> names are no longer
// callable. The single MCP execution contract is mcp_call with a ref
// (<server>:<tool>) from mcp_search / tool_list.
