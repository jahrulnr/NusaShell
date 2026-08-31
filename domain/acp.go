package domain

import (
	"errors"
	"fmt"
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

// AcpTransportKind selects how NusaShell talks to an ACP agent.
const (
	AcpTransportStdio  = "stdio"  // local subprocess (default)
	AcpTransportRemote = "remote" // cloud agent over WebSocket/HTTP
)

// TrustLevelToRiskTierCap maps a workflow TrustLevel to the maximum RiskTier
// that headless (pipeline) agent steps and ACP subagents spawned from them
// may reach. This enforces the "unattended runs must not silently reach
// bypass" rule: only an explicit privileged workflow can unlock bypass.
func TrustLevelToRiskTierCap(trust TrustLevel) RiskTier {
	switch trust {
	case TrustPrivileged:
		return RiskBypass
	case TrustTrusted:
		return RiskEditConfirmed
	default:
		return RiskReadOnly
	}
}

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
// hardcoded: command + args + env is the whole identity. For remote
// (cloud) agents, Command holds the WebSocket/HTTP endpoint URL and Args
// is empty; Transport selects the dial mode.
type AcpAgent struct {
	ID                 string
	Name               string
	Command            string
	Args               []string
	Env                map[string]string
	Transport          string
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

// EffectiveTransport returns the transport kind, defaulting to stdio for
// empty or unrecognized values (fail-safe).
func (a *AcpAgent) EffectiveTransport() string {
	switch a.Transport {
	case AcpTransportRemote:
		return AcpTransportRemote
	default:
		return AcpTransportStdio
	}
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

// AcpTranscriptChunk is one live update from session/update, or a parent
// prompt sent to the ACP session. Prompt chunks keep steering visible in the
// persisted transcript alongside the agent's response.
type AcpTranscriptChunk struct {
	Kind       string // prompt | text | thought | tool | plan | status | usage
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
// Consecutive text and thought chunks are merged into a single chunk so
// streaming agent_message_chunk updates (often one char/token each) do not
// produce one transcript line per delta.
func (r *AcpRun) AppendTranscript(chunk AcpTranscriptChunk) {
	switch chunk.Kind {
	case "text", "thought":
		if r.mergeStreamingChunk(chunk) {
			return
		}
	case "usage":
		if r.replaceTranscriptChunk("usage", "", chunk) {
			return
		}
	case "tool":
		if chunk.ToolID != "" && r.replaceTranscriptChunk("tool", chunk.ToolID, chunk) {
			return
		}
	}
	r.Transcript = append(r.Transcript, chunk)
	r.trimTranscriptCap()
}

func (r *AcpRun) mergeStreamingChunk(chunk AcpTranscriptChunk) bool {
	lastContent := len(r.Transcript) - 1
	for lastContent >= 0 && r.Transcript[lastContent].Kind == "usage" {
		lastContent--
	}
	if lastContent < 0 || r.Transcript[lastContent].Kind != chunk.Kind {
		return false
	}
	r.Transcript[lastContent].Text += chunk.Text
	if !chunk.At.IsZero() {
		r.Transcript[lastContent].At = chunk.At
	}
	r.trimTranscriptCap()
	return true
}

func (r *AcpRun) replaceTranscriptChunk(kind, toolID string, chunk AcpTranscriptChunk) bool {
	for i := len(r.Transcript) - 1; i >= 0; i-- {
		existing := &r.Transcript[i]
		if existing.Kind != kind || (toolID != "" && existing.ToolID != toolID) {
			continue
		}
		if kind == "tool" {
			mergeToolTranscript(existing, chunk)
		} else {
			*existing = chunk
		}
		r.trimTranscriptCap()
		return true
	}
	return false
}

func mergeToolTranscript(existing *AcpTranscriptChunk, update AcpTranscriptChunk) {
	if update.Text != "" {
		existing.Text = update.Text
	}
	if update.ToolTitle != "" {
		existing.ToolTitle = update.ToolTitle
	}
	if update.ToolKind != "" {
		existing.ToolKind = update.ToolKind
	}
	if update.ToolStatus != "" {
		existing.ToolStatus = update.ToolStatus
	}
	if !update.At.IsZero() {
		existing.At = update.At
	}
}

// trimTranscriptCap drops chunks from the front until the total text size is
// under MaxAcpTranscriptBytes.
func (r *AcpRun) trimTranscriptCap() {
	total := 0
	for _, c := range r.Transcript {
		total += transcriptChunkSize(c)
	}
	for total > MaxAcpTranscriptBytes && len(r.Transcript) > 1 {
		dropped := r.Transcript[0]
		total -= transcriptChunkSize(dropped)
		r.Transcript = r.Transcript[1:]
	}
	if total <= MaxAcpTranscriptBytes || len(r.Transcript) == 0 {
		return
	}
	chunk := &r.Transcript[0]
	titleSize := utf8.RuneCountInString(chunk.ToolTitle)
	if titleSize >= MaxAcpTranscriptBytes {
		chunk.ToolTitle = runeTail(chunk.ToolTitle, MaxAcpTranscriptBytes)
		chunk.Text = ""
		return
	}
	chunk.Text = runeTail(chunk.Text, MaxAcpTranscriptBytes-titleSize)
}

func transcriptChunkSize(chunk AcpTranscriptChunk) int {
	return utf8.RuneCountInString(chunk.Text) + utf8.RuneCountInString(chunk.ToolTitle)
}

func runeTail(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
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

// BeginRunning transitions the run to running and stamps UpdatedAt. Used
// on session start, steer, and resume after permission.
func (r *AcpRun) BeginRunning(now time.Time) {
	if r == nil {
		return
	}
	r.Status = AcpRunRunning
	r.UpdatedAt = now
}

// Finish transitions the run to a terminal status, records error and stop
// reason, clears any pending permission, and stamps EndedAt + UpdatedAt.
func (r *AcpRun) Finish(status AcpRunStatus, errMsg, stop string, now time.Time) {
	if r == nil {
		return
	}
	r.Status = status
	r.Error = errMsg
	r.StopReason = stop
	r.PendingPermission = nil
	r.EndedAt = now
	r.UpdatedAt = now
}

// ResolvePermission clears the pending permission and, if the run was
// waiting for permission, transitions it back to running.
func (r *AcpRun) ResolvePermission(now time.Time) {
	if r == nil {
		return
	}
	r.PendingPermission = nil
	if r.Status == AcpRunWaitingPermission {
		r.Status = AcpRunRunning
	}
	r.UpdatedAt = now
}

// PathRooted reports whether p is an absolute location that must not be
// joined onto a workspace prefix. On Windows, slash-rooted paths such as
// `\etc\passwd` and Unix-style `/etc/passwd` are rooted even though
// filepath.IsAbs is false without a volume.
func PathRooted(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	return strings.HasPrefix(filepath.ToSlash(p), "/")
}

// hostRootedPath reports whether p names a host location rather than a
// relative workspace or plugin path. Slash-rooted, backslash-rooted, and
// Windows volume paths (`C:\…`) are rooted on every GOOS so validation
// fixtures stay portable.
func hostRootedPath(p string) bool {
	if PathRooted(p) || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	}
	return false
}

// ResolveWithinWorkspace joins relative paths onto workspace and reports
// whether the result stays inside workspace. Rooted paths are compared as-is.
func ResolveWithinWorkspace(workspace, path string) (string, bool) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(path) == "" {
		return "", false
	}
	root := filepath.Clean(workspace)
	clean := filepath.Clean(path)
	if !PathRooted(clean) {
		clean = filepath.Clean(filepath.Join(root, clean))
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return clean, true
}

func pathsWithinWorkspace(paths []string, workspace string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if _, ok := ResolveWithinWorkspace(workspace, p); !ok {
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

// WrapSessionAuthError upgrades session/new failures when the agent advertised
// auth methods but Providers never stored an AuthMethodID (CLI not logged in).
func WrapSessionAuthError(agent *AcpAgent, err error) error {
	if err == nil || agent == nil {
		return err
	}
	if strings.TrimSpace(agent.AuthMethodID) != "" {
		return err
	}
	if len(agent.CachedAuthMethods) == 0 {
		return err
	}
	if !IsSessionAuthRequired(err) {
		return err
	}
	methods := make([]string, 0, len(agent.CachedAuthMethods))
	for _, m := range agent.CachedAuthMethods {
		if id := strings.TrimSpace(m.ID); id != "" {
			methods = append(methods, id)
		}
	}
	if len(methods) == 0 {
		return err
	}
	return fmt.Errorf(
		"ACP agent %q is not authenticated — use Authenticate in Providers (methods: %s) before spawning or refreshing catalog: %v",
		agent.Name,
		strings.Join(methods, ", "),
		err,
	)
}

// IsSessionAuthRequired reports whether err indicates the ACP agent needs
// authentication before session/new can succeed. Exported so the runtime
// can decide whether to retry with Authenticate (lazy auth).
// ACP reserves JSON-RPC code -32000 for auth-required, so a typed error
// carrying that code is authoritative; message matching stays as the
// fallback for agents that reply with other codes.
func IsSessionAuthRequired(err error) bool {
	var coded interface{ ErrorCode() int }
	if errors.As(err, &coded) && coded.ErrorCode() == -32000 {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "authentication required") ||
		strings.Contains(msg, "not authenticated") ||
		strings.Contains(msg, "not logged in") ||
		strings.Contains(msg, "run 'agent login'")
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
