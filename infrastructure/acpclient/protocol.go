package acpclient

import (
	"encoding/json"
	"fmt"
)

// Protocol types for Agent Client Protocol JSON-RPC 2.0 over stdio.
// Field names follow ACP camelCase.

const ProtocolVersion = 1

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCError is a typed JSON-RPC error reply from the agent side. ACP
// reserves code -32000 for auth-required; carrying the code lets callers
// detect it without matching message text.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string { return fmt.Sprintf("acp %s", e.Message) }

// ErrorCode exposes the JSON-RPC error code for errors.As inspection.
func (e *RPCError) ErrorCode() int { return e.Code }

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         Implementation     `json:"clientInfo"`
}

type ClientCapabilities struct {
	FS       *FSCapabilities `json:"fs,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
}

type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         Implementation    `json:"agentInfo"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
}

type AgentCapabilities struct {
	LoadSession        bool        `json:"loadSession"`
	PromptCapabilities *PromptCaps `json:"promptCapabilities,omitempty"`
	MCPCapabilities    *MCPCaps    `json:"mcpCapabilities,omitempty"`
}

type PromptCaps struct {
	Image           bool `json:"image"`
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type MCPCaps struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type AuthenticateParams struct {
	MethodID string `json:"methodId"`
}

type NewSessionParams struct {
	Cwd        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

type NewSessionResult struct {
	SessionID string         `json:"sessionId"`
	Modes     *SessionModes  `json:"modes,omitempty"`
	Models    *SessionModels `json:"models,omitempty"`
	// ConfigOptions is the v1 session-configuration selector list. Agents
	// in the OpenCode generation replaced the legacy modes/models payload
	// with these selectors; NusaShell folds select-type mode/model options
	// onto its existing caches and keeps driving them through the still
	// supported legacy set_mode/set_model methods.
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ConfigOption is one session configuration selector. Boolean-typed
// options exist alongside selects, so CurrentValue stays raw JSON.
type ConfigOption struct {
	ID           string               `json:"id"`
	Name         string               `json:"name,omitempty"`
	Category     string               `json:"category,omitempty"` // mode | model | thought_level | ...
	Type         string               `json:"type,omitempty"`     // select | boolean
	CurrentValue json.RawMessage      `json:"currentValue,omitempty"`
	Options      []ConfigChoiceOption `json:"options,omitempty"`
}

// ConfigChoiceOption is one selectable value of a select config option.
type ConfigChoiceOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// StringValue decodes a quoted-string config option value. Absent or
// non-string values (booleans) decode to "".
func (o ConfigOption) StringValue() string {
	if len(o.CurrentValue) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(o.CurrentValue, &s); err != nil {
		return ""
	}
	return s
}

type SessionModes struct {
	CurrentModeID  string `json:"currentModeId"`
	AvailableModes []Mode `json:"availableModes"`
}

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModels struct {
	CurrentModelID  string      `json:"currentModelId"`
	AvailableModels []ModelInfo `json:"availableModels"`
}

type ModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

type SetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type SetModelResult struct {
	Models *SessionModels `json:"models,omitempty"`
}

type SessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

type SessionUpdate struct {
	SessionUpdate string         `json:"sessionUpdate"`
	Content       *ContentBlock  `json:"content,omitempty"`
	ToolCallID    string         `json:"toolCallId,omitempty"`
	Title         string         `json:"title,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Status        string         `json:"status,omitempty"`
	Locations     []ToolLocation `json:"locations,omitempty"`
	CurrentModeID string         `json:"currentModeId,omitempty"`
	Entries       []PlanEntry    `json:"entries,omitempty"`
	Used          int            `json:"used,omitempty"`
	Size          int            `json:"size,omitempty"`
}

type ToolLocation struct {
	Path string `json:"path"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionToolCall struct {
	ToolCallID string         `json:"toolCallId"`
	Title      string         `json:"title"`
	Kind       string         `json:"kind,omitempty"`
	Locations  []ToolLocation `json:"locations,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

type PermissionOutcome struct {
	Outcome  string `json:"outcome"` // selected | cancelled
	OptionID string `json:"optionId,omitempty"`
}

type ReadTextFileParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Path      string `json:"path"`
	Line      int    `json:"line,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type ReadTextFileResult struct {
	Content string `json:"content"`
}

type WriteTextFileParams struct {
	SessionID string `json:"sessionId,omitempty"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}
