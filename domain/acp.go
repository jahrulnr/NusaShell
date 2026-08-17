package domain

import (
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// RiskTier is NusaShell's internal permission posture for an ACP session.
// It is independent of vendor mode IDs.
type RiskTier string

const (
	RiskReadOnly      RiskTier = "read_only"
	RiskEditConfirmed RiskTier = "edit_confirmed"
	RiskBypass        RiskTier = "bypass"
)

// AcpRunStatus is the lifecycle of one spawned ACP subagent run.
type AcpRunStatus string

const (
	AcpRunStarting          AcpRunStatus = "starting"
	AcpRunRunning           AcpRunStatus = "running"
	AcpRunWaitingPermission AcpRunStatus = "waiting_permission"
	AcpRunCompleted         AcpRunStatus = "completed"
	AcpRunFailed            AcpRunStatus = "failed"
	AcpRunCancelled         AcpRunStatus = "cancelled"
)

// ModelSelectionStatus records whether a preferred model was actually applied.
type ModelSelectionStatus string

const (
	ModelSelectionNone       ModelSelectionStatus = ""
	ModelSelectionConfirmed  ModelSelectionStatus = "confirmed"
	ModelSelectionRejected   ModelSelectionStatus = "rejected"
	ModelSelectionUnverified ModelSelectionStatus = "unverified"
)

// PermissionOutcome is the client's reply to session/request_permission.
type PermissionOutcome string

const (
	PermissionAllowOnce    PermissionOutcome = "allow_once"
	PermissionAllowSession PermissionOutcome = "allow_session"
	PermissionDeny         PermissionOutcome = "deny"
	PermissionCancelled    PermissionOutcome = "cancelled"
)

const (
	// MaxAcpSpawnCount caps fan-out from a single subagent tool call.
	MaxAcpSpawnCount = 6
	// MaxAcpConcurrentRuns caps live ACP sessions process-wide.
	MaxAcpConcurrentRuns = 8
	// DefaultAcpPermissionTimeout is the fail-closed wait for a user decision.
	DefaultAcpPermissionTimeout = 2 * time.Minute
	// MaxAcpTranscriptBytes caps in-memory live transcript per run.
	MaxAcpTranscriptBytes = 256 * 1024
	// MaxAcpPermissionPaths is how many paths to keep on a permission event.
	MaxAcpPermissionPaths = 12
)

// AcpAgent is a generic ACP subprocess configuration. Vendors are not
// hardcoded: command + args + env is the whole identity.
type AcpAgent struct {
	ID                 string
	Name               string
	Command            string
	Args               []string
	Env                map[string]string
	Enabled            bool
	PreferredModelID   string
	PreferredModeID    string
	ModeRiskMappings   []ModeRiskMapping
	DefaultWorkspace   string
	AuthMethodID       string
	CachedAuthMethods  []AcpAuthMethod
	CachedCapabilities AcpCapabilities
	CachedModes        []AcpMode
	CachedModels       []AcpModelInfo
	UpdatedAt          time.Time
}

// ModeRiskMapping maps one agent-advertised mode ID onto an internal risk tier.
type ModeRiskMapping struct {
	ModeID string
	Tier   RiskTier
}

// AcpAuthMethod is advertised by the agent at initialize.
type AcpAuthMethod struct {
	ID          string
	Name        string
	Description string
}

// AcpCapabilities is the subset of agentCapabilities NusaShell cares about.
type AcpCapabilities struct {
	LoadSession bool
	HasModes    bool
	HasMCP      bool
	HasFS       bool
}

// AcpMode is one session mode advertised by the agent.
type AcpMode struct {
	ID          string
	Name        string
	Description string
}

// AcpModelInfo is one model advertised by the agent.
type AcpModelInfo struct {
	ID          string
	Name        string
	Description string
	Tier        ModelTier
}

// ModelTier is a local quality/cost overlay. ACP does not provide this.
type ModelTier string

const (
	ModelTierFrontier     ModelTier = "frontier"
	ModelTierBalanced     ModelTier = "balanced"
	ModelTierEconomy      ModelTier = "economy"
	ModelTierUnclassified ModelTier = "unclassified"
)

// AcpRun is one spawned subagent session. It is in-memory only.
type AcpRun struct {
	ID                   string
	AgentID              string
	AgentName            string
	ConversationID       string
	ParentToolCallID     string
	SessionID            string
	Workspace            string
	Prompt               string
	Status               AcpRunStatus
	CurrentModeID        string
	AvailableModes       []AcpMode
	CurrentModelID       string
	ModelSelectionStatus ModelSelectionStatus
	RiskTier             RiskTier
	StopReason           string
	Error                string
	Transcript           []AcpTranscriptChunk
	PendingPermission    *AcpPermissionRequest
	QueuedSteer          string
	StartedAt            time.Time
	UpdatedAt            time.Time
	EndedAt              time.Time
}

// AcpTranscriptChunk is one live update from session/update.
type AcpTranscriptChunk struct {
	Kind       string // text | thought | tool | plan | status | usage
	Text       string
	ToolID     string
	ToolTitle  string
	ToolKind   string
	ToolStatus string
	At         time.Time
}

// AcpPermissionRequest is a blocking session/request_permission.
type AcpPermissionRequest struct {
	ID          string
	SessionID   string
	ToolTitle   string
	ToolKind    string
	Paths       []string
	Options     []AcpPermissionOption
	RequestedAt time.Time
}

// AcpPermissionOption is one choice the agent offered.
type AcpPermissionOption struct {
	ID   string
	Name string
	Kind string // allow_once | allow_always | reject_once | reject_always
}

// IsValidRiskTier reports whether t is a known risk tier.
func IsValidRiskTier(t RiskTier) bool {
	switch t {
	case RiskReadOnly, RiskEditConfirmed, RiskBypass:
		return true
	}
	return false
}

// InferRiskTier maps a vendor mode ID onto a risk tier. Explicit mappings
// win. Unknown IDs are read_only (fail-safe), never bypass.
func InferRiskTier(modeID string, mappings []ModeRiskMapping) RiskTier {
	id := strings.TrimSpace(modeID)
	for _, m := range mappings {
		if m.ModeID == id && IsValidRiskTier(m.Tier) {
			return m.Tier
		}
	}
	lower := strings.ToLower(id)
	switch {
	case lower == "":
		return RiskReadOnly
	case containsAny(lower, "bypass", "yolo"):
		return RiskBypass
	case containsAny(lower, "plan", "architect", "ask"):
		return RiskReadOnly
	case containsAny(lower, "accept", "code", "edit", "default"):
		return RiskEditConfirmed
	default:
		return RiskReadOnly
	}
}

// StrictestAvailableMode returns the most restrictive advertised mode,
// preferring an explicit read_only mapping. Empty when the agent has no modes.
func StrictestAvailableMode(modes []AcpMode, mappings []ModeRiskMapping) string {
	if len(modes) == 0 {
		return ""
	}
	rank := map[RiskTier]int{
		RiskReadOnly:      0,
		RiskEditConfirmed: 1,
		RiskBypass:        2,
	}
	best := modes[0]
	bestRank := rank[InferRiskTier(best.ID, mappings)]
	for _, m := range modes[1:] {
		r := rank[InferRiskTier(m.ID, mappings)]
		if r < bestRank {
			best = m
			bestRank = r
		}
	}
	return best.ID
}

// SeedModeRiskMappings fills missing mode IDs with inferred tiers.
func SeedModeRiskMappings(modes []AcpMode, existing []ModeRiskMapping) []ModeRiskMapping {
	byID := map[string]RiskTier{}
	for _, m := range existing {
		if m.ModeID != "" && IsValidRiskTier(m.Tier) {
			byID[m.ModeID] = m.Tier
		}
	}
	out := make([]ModeRiskMapping, 0, len(modes))
	for _, mode := range modes {
		if mode.ID == "" {
			continue
		}
		if tier, ok := byID[mode.ID]; ok {
			out = append(out, ModeRiskMapping{ModeID: mode.ID, Tier: tier})
			continue
		}
		out = append(out, ModeRiskMapping{ModeID: mode.ID, Tier: InferRiskTier(mode.ID, nil)})
	}
	return out
}

// ClassifyModelTier applies a local overlay. Unmatched models stay unclassified.
func ClassifyModelTier(modelID, name string) ModelTier {
	hay := strings.ToLower(modelID + " " + name)
	switch {
	case containsAny(hay, "opus", "gpt-5", "gpt-4.5", "gemini-3", "gemini-2.5-pro", "o3", "o1"):
		return ModelTierFrontier
	case containsAny(hay, "mini", "flash", "haiku", "nano", "lite", "fast"):
		return ModelTierEconomy
	case containsAny(hay, "sonnet", "gpt-4", "gemini"):
		return ModelTierBalanced
	default:
		return ModelTierUnclassified
	}
}

// PermissionAutoDecision is the policy result before prompting the user.
type PermissionAutoDecision struct {
	Auto    bool
	Outcome PermissionOutcome
	Reason  string
}

// DecideAcpPermission applies the risk-tier policy. Audit happens at the
// caller, before this result is acted on.
func DecideAcpPermission(tier RiskTier, toolKind string, paths []string, workspace string) PermissionAutoDecision {
	kind := strings.ToLower(strings.TrimSpace(toolKind))
	if !IsValidRiskTier(tier) {
		tier = RiskReadOnly
	}
	if tier == RiskBypass {
		return PermissionAutoDecision{Auto: true, Outcome: PermissionAllowOnce, Reason: "bypass"}
	}
	readLike := kind == "" || kind == "read" || kind == "search" || kind == "think" || kind == "fetch"
	if tier == RiskReadOnly && readLike {
		return PermissionAutoDecision{Auto: true, Outcome: PermissionAllowOnce, Reason: "read_only_read"}
	}
	if tier == RiskEditConfirmed && (kind == "edit" || kind == "delete" || kind == "move") && pathsWithinWorkspace(paths, workspace) {
		return PermissionAutoDecision{Auto: true, Outcome: PermissionAllowOnce, Reason: "edit_confirmed_workspace"}
	}
	return PermissionAutoDecision{Auto: false, Reason: "prompt_user"}
}

// SamplePermissionPaths keeps a compact sample for the UI.
func SamplePermissionPaths(paths []string) []string {
	if len(paths) <= MaxAcpPermissionPaths {
		return append([]string(nil), paths...)
	}
	out := append([]string(nil), paths[:MaxAcpPermissionPaths]...)
	out = append(out, "…")
	return out
}

// AppendTranscript adds a chunk and drops from the front when over the cap.
func (r *AcpRun) AppendTranscript(chunk AcpTranscriptChunk) {
	r.Transcript = append(r.Transcript, chunk)
	total := 0
	for _, c := range r.Transcript {
		total += utf8.RuneCountInString(c.Text) + utf8.RuneCountInString(c.ToolTitle)
	}
	for total > MaxAcpTranscriptBytes && len(r.Transcript) > 1 {
		dropped := r.Transcript[0]
		total -= utf8.RuneCountInString(dropped.Text) + utf8.RuneCountInString(dropped.ToolTitle)
		r.Transcript = r.Transcript[1:]
	}
}

// Live reports whether the run still occupies a process/session.
func (r *AcpRun) Live() bool {
	if r == nil {
		return false
	}
	switch r.Status {
	case AcpRunStarting, AcpRunRunning, AcpRunWaitingPermission:
		return true
	}
	return false
}

func pathsWithinWorkspace(paths []string, workspace string) bool {
	if strings.TrimSpace(workspace) == "" {
		return false
	}
	root := filepath.Clean(workspace)
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		clean := filepath.Clean(p)
		if !filepath.IsAbs(clean) {
			clean = filepath.Join(root, clean)
		}
		rel, err := filepath.Rel(root, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false
		}
	}
	return true
}

func containsAny(hay string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// RedactEnvKeys returns env keys with values stripped for wire/log use.
func RedactEnvKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	return keys
}

// ValidateAcpAgentSave checks required fields for a generic ACP registration.
func ValidateAcpAgentSave(name, command string) string {
	if strings.TrimSpace(name) == "" {
		return "agent name is required"
	}
	if strings.TrimSpace(command) == "" {
		return "command is required"
	}
	return ""
}
