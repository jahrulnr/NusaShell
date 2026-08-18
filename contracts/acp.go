package contracts

// ---- ACP agents (spawn-only subagents) ----

type AcpAgentDTO struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Command            string             `json:"command"`
	Args               []string           `json:"args,omitempty"`
	EnvKeys            []string           `json:"env_keys,omitempty"`
	Transport          string             `json:"transport,omitempty"`
	Enabled            bool               `json:"enabled"`
	PreferredModelID   string             `json:"preferred_model_id,omitempty"`
	PreferredModeID    string             `json:"preferred_mode_id,omitempty"`
	ModeRiskMappings   []AcpModeRiskDTO   `json:"mode_risk_mappings,omitempty"`
	DefaultWorkspace   string             `json:"default_workspace,omitempty"`
	AuthMethodID       string             `json:"auth_method_id,omitempty"`
	CachedAuthMethods  []AcpAuthMethodDTO `json:"auth_methods,omitempty"`
	CachedCapabilities AcpCapabilitiesDTO `json:"capabilities"`
	CachedModes        []AcpModeDTO       `json:"modes,omitempty"`
	CachedModels       []AcpModelDTO      `json:"models,omitempty"`
	UpdatedAt          string             `json:"updated_at,omitempty"`
}

type AcpModeRiskDTO struct {
	ModeID string `json:"mode_id"`
	Tier   string `json:"tier"`
}

type AcpAuthMethodDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type AcpCapabilitiesDTO struct {
	LoadSession bool `json:"load_session"`
	HasModes    bool `json:"has_modes"`
	HasMCP      bool `json:"has_mcp"`
	HasFS       bool `json:"has_fs"`
}

type AcpModeDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	RiskTier    string `json:"risk_tier,omitempty"`
}

type AcpModelDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Tier        string `json:"tier,omitempty"`
}

type AcpAgentsListResult struct {
	Agents []AcpAgentDTO `json:"agents"`
}

type AcpAgentSaveRequest struct {
	ID               string            `json:"id,omitempty"`
	Name             string            `json:"name"`
	Command          string            `json:"command"`
	Args             []string          `json:"args,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Transport        string            `json:"transport,omitempty"`
	Enabled          *bool             `json:"enabled,omitempty"`
	PreferredModelID string            `json:"preferred_model_id,omitempty"`
	PreferredModeID  string            `json:"preferred_mode_id,omitempty"`
	ModeRiskMappings []AcpModeRiskDTO  `json:"mode_risk_mappings,omitempty"`
	DefaultWorkspace string            `json:"default_workspace,omitempty"`
}

type AcpAgentIDRequest struct {
	ID string `json:"id"`
}

type AcpAuthenticateRequest struct {
	ID       string `json:"id"`
	MethodID string `json:"method_id"`
}

type AcpProbeResult struct {
	Agent AcpAgentDTO `json:"agent"`
	OK    bool        `json:"ok"`
	Error string      `json:"error,omitempty"`
}

type AcpRunDTO struct {
	ID                   string                  `json:"id"`
	AgentID              string                  `json:"agent_id"`
	AgentName            string                  `json:"agent_name"`
	ConversationID       string                  `json:"conversation_id,omitempty"`
	ParentToolCallID     string                  `json:"parent_tool_call_id,omitempty"`
	SessionID            string                  `json:"session_id,omitempty"`
	Workspace            string                  `json:"workspace,omitempty"`
	Prompt               string                  `json:"prompt,omitempty"`
	Status               string                  `json:"status"`
	CurrentModeID        string                  `json:"current_mode_id,omitempty"`
	AvailableModes       []AcpModeDTO            `json:"available_modes,omitempty"`
	CurrentModelID       string                  `json:"current_model_id,omitempty"`
	ModelSelectionStatus string                  `json:"model_selection_status,omitempty"`
	RiskTier             string                  `json:"risk_tier,omitempty"`
	StopReason           string                  `json:"stop_reason,omitempty"`
	Error                string                  `json:"error,omitempty"`
	Transcript           []AcpTranscriptChunkDTO `json:"transcript,omitempty"`
	PendingPermission    *AcpPermissionDTO       `json:"pending_permission,omitempty"`
	QueuedSteer          string                  `json:"queued_steer,omitempty"`
	StartedAt            string                  `json:"started_at,omitempty"`
	UpdatedAt            string                  `json:"updated_at,omitempty"`
	EndedAt              string                  `json:"ended_at,omitempty"`
}

type AcpTranscriptChunkDTO struct {
	Kind       string `json:"kind"`
	Text       string `json:"text,omitempty"`
	ToolID     string `json:"tool_id,omitempty"`
	ToolTitle  string `json:"tool_title,omitempty"`
	ToolKind   string `json:"tool_kind,omitempty"`
	ToolStatus string `json:"tool_status,omitempty"`
	At         string `json:"at,omitempty"`
}

type AcpPermissionDTO struct {
	ID          string                   `json:"id"`
	SessionID   string                   `json:"session_id,omitempty"`
	ToolTitle   string                   `json:"tool_title"`
	ToolKind    string                   `json:"tool_kind,omitempty"`
	Paths       []string                 `json:"paths,omitempty"`
	PathCount   int                      `json:"path_count,omitempty"`
	Options     []AcpPermissionOptionDTO `json:"options,omitempty"`
	RequestedAt string                   `json:"requested_at,omitempty"`
}

type AcpPermissionOptionDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type AcpRunsListRequest struct {
	ConversationID string `json:"conversation_id,omitempty"`
}

type AcpRunsListResult struct {
	Runs []AcpRunDTO `json:"runs"`
}

type AcpRunIDRequest struct {
	ID string `json:"id"`
}

type AcpRunSteerRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type AcpRunWaitRequest struct {
	ID        string `json:"id"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type AcpRunPromoteRequest struct {
	ID   string `json:"id"`
	Tier string `json:"tier"`
}

type AcpRunSetModeRequest struct {
	ID     string `json:"id"`
	ModeID string `json:"mode_id"`
}

type AcpPermissionDecideRequest struct {
	RunID    string `json:"run_id"`
	ID       string `json:"id"`
	OptionID string `json:"option_id"`
	Outcome  string `json:"outcome,omitempty"`
}

type AcpRunEvent struct {
	Run AcpRunDTO `json:"run"`
}

type AcpPermissionEvent struct {
	RunID      string           `json:"run_id"`
	Permission AcpPermissionDTO `json:"permission"`
}

type AcpPermissionDecidedEvent struct {
	RunID    string `json:"run_id"`
	ID       string `json:"id"`
	Outcome  string `json:"outcome"`
	OptionID string `json:"option_id,omitempty"`
}

type AcpModeChangedEvent struct {
	RunID  string `json:"run_id"`
	ModeID string `json:"mode_id"`
	Source string `json:"source,omitempty"` // user | agent
}
