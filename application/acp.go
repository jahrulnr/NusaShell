package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) acpAgentDTO(agent *domain.AcpAgent) contracts.AcpAgentDTO {
	dto := contracts.AcpAgentDTO{
		ID:               agent.ID,
		Name:             agent.Name,
		Command:          agent.Command,
		Args:             agent.Args,
		EnvKeys:          domain.RedactEnvKeys(agent.Env),
		Transport:        agent.EffectiveTransport(),
		Enabled:          agent.Enabled,
		PreferredModelID: agent.PreferredModelID,
		PreferredModeID:  agent.PreferredModeID,
		DefaultWorkspace: agent.DefaultWorkspace,
		AuthMethodID:     agent.AuthMethodID,
		CachedCapabilities: contracts.AcpCapabilitiesDTO{
			LoadSession: agent.CachedCapabilities.LoadSession,
			HasModes:    agent.CachedCapabilities.HasModes,
			HasMCP:      agent.CachedCapabilities.HasMCP,
			HasFS:       true,
		},
	}
	if !agent.UpdatedAt.IsZero() {
		dto.UpdatedAt = agent.UpdatedAt.Format(timeRFC3339)
	}
	for _, m := range agent.ModeRiskMappings {
		dto.ModeRiskMappings = append(dto.ModeRiskMappings, contracts.AcpModeRiskDTO{ModeID: m.ModeID, Tier: string(m.Tier)})
	}
	for _, m := range agent.CachedAuthMethods {
		dto.CachedAuthMethods = append(dto.CachedAuthMethods, contracts.AcpAuthMethodDTO{ID: m.ID, Name: m.Name, Description: m.Description})
	}
	for _, m := range agent.CachedModes {
		dto.CachedModes = append(dto.CachedModes, contracts.AcpModeDTO{
			ID: m.ID, Name: m.Name, Description: m.Description,
			RiskTier: string(domain.InferRiskTier(m.ID, agent.ModeRiskMappings)),
		})
	}
	for _, m := range agent.CachedModels {
		dto.CachedModels = append(dto.CachedModels, contracts.AcpModelDTO{
			ID: m.ID, Name: m.Name, Description: m.Description, Tier: string(m.Tier),
		})
	}
	return dto
}

func acpRunDTO(run *domain.AcpRun) contracts.AcpRunDTO {
	dto := contracts.AcpRunDTO{
		ID:                   run.ID,
		AgentID:              run.AgentID,
		AgentName:            run.AgentName,
		ConversationID:       run.ConversationID,
		ParentToolCallID:     run.ParentToolCallID,
		SessionID:            run.SessionID,
		Workspace:            run.Workspace,
		Prompt:               run.Prompt,
		Status:               string(run.Status),
		CurrentModeID:        run.CurrentModeID,
		CurrentModelID:       run.CurrentModelID,
		ModelSelectionStatus: string(run.ModelSelectionStatus),
		RiskTier:             string(run.RiskTier),
		StopReason:           run.StopReason,
		Error:                run.Error,
		QueuedSteer:          run.QueuedSteer,
	}
	if !run.StartedAt.IsZero() {
		dto.StartedAt = run.StartedAt.Format(timeRFC3339)
	}
	if !run.UpdatedAt.IsZero() {
		dto.UpdatedAt = run.UpdatedAt.Format(timeRFC3339)
	}
	if !run.EndedAt.IsZero() {
		dto.EndedAt = run.EndedAt.Format(timeRFC3339)
	}
	for _, m := range run.AvailableModes {
		dto.AvailableModes = append(dto.AvailableModes, contracts.AcpModeDTO{
			ID: m.ID, Name: m.Name, Description: m.Description,
			RiskTier: string(domain.InferRiskTier(m.ID, nil)),
		})
	}
	for _, c := range run.Transcript {
		chunk := contracts.AcpTranscriptChunkDTO{
			Kind: c.Kind, Text: c.Text, ToolID: c.ToolID, ToolTitle: c.ToolTitle,
			ToolKind: c.ToolKind, ToolStatus: c.ToolStatus,
		}
		if !c.At.IsZero() {
			chunk.At = c.At.Format(timeRFC3339)
		}
		dto.Transcript = append(dto.Transcript, chunk)
	}
	if run.PendingPermission != nil {
		p := run.PendingPermission
		dto.PendingPermission = &contracts.AcpPermissionDTO{
			ID: p.ID, SessionID: p.SessionID, ToolTitle: p.ToolTitle, ToolKind: p.ToolKind,
			Paths: p.Paths, PathCount: len(p.Paths),
		}
		if !p.RequestedAt.IsZero() {
			dto.PendingPermission.RequestedAt = p.RequestedAt.Format(timeRFC3339)
		}
		for _, o := range p.Options {
			dto.PendingPermission.Options = append(dto.PendingPermission.Options, contracts.AcpPermissionOptionDTO{
				ID: o.ID, Name: o.Name, Kind: o.Kind,
			})
		}
	}
	return dto
}

func (a *App) handleAcpAgentsList() (any, *contracts.RPCError) {
	if a.AcpAgents == nil {
		return contracts.AcpAgentsListResult{Agents: []contracts.AcpAgentDTO{}}, nil
	}
	list := a.AcpAgents.List()
	out := make([]contracts.AcpAgentDTO, 0, len(list))
	for _, agent := range list {
		out = append(out, a.acpAgentDTO(agent))
	}
	return contracts.AcpAgentsListResult{Agents: out}, nil
}

func (a *App) handleAcpAgentsSave(req contracts.AcpAgentSaveRequest) (any, *contracts.RPCError) {
	if a.AcpAgents == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP agent store is not available"}
	}
	if msg := domain.ValidateAcpAgentSave(req.Name, req.Command); msg != "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: msg}
	}
	var agent *domain.AcpAgent
	if req.ID != "" {
		existing, err := a.AcpAgents.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		if existing.Command != "" && existing.Command != strings.TrimSpace(req.Command) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "command is immutable after registration; delete and recreate the agent to change it"}
		}
		agent = existing
	} else {
		agent = &domain.AcpAgent{ID: domain.NewID("acp"), Enabled: true}
	}
	agent.Name = strings.TrimSpace(req.Name)
	agent.Command = strings.TrimSpace(req.Command)
	agent.Transport = strings.TrimSpace(req.Transport)
	if req.Args != nil {
		agent.Args = req.Args
	}
	if req.Env != nil {
		agent.Env = req.Env
	}
	if req.Enabled != nil {
		agent.Enabled = *req.Enabled
	} else if req.ID == "" {
		agent.Enabled = true
	}
	agent.PreferredModelID = strings.TrimSpace(req.PreferredModelID)
	agent.PreferredModeID = strings.TrimSpace(req.PreferredModeID)
	agent.DefaultWorkspace = strings.TrimSpace(req.DefaultWorkspace)
	if len(req.ModeRiskMappings) > 0 {
		agent.ModeRiskMappings = nil
		for _, m := range req.ModeRiskMappings {
			tier := domain.RiskTier(m.Tier)
			if !domain.IsValidRiskTier(tier) {
				tier = domain.RiskReadOnly
			}
			agent.ModeRiskMappings = append(agent.ModeRiskMappings, domain.ModeRiskMapping{ModeID: m.ModeID, Tier: tier})
		}
	}
	agent.UpdatedAt = time.Now().UTC()
	if err := a.AcpAgents.Save(agent); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "acp", "acp agent saved: %s", agent.Name)
	return contracts.AcpAgentsListResult{Agents: []contracts.AcpAgentDTO{a.acpAgentDTO(agent)}}, nil
}

func (a *App) handleAcpAgentsDelete(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	if a.AcpAgents == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP agent store is not available"}
	}
	if _, err := a.AcpAgents.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.AcpAgents.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "acp", "acp agent deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleAcpAgentsProbe(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := a.acpReady(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	updated, err := rt.Probe(ctx, agent)
	if err != nil {
		a.log("warn", "acp", "probe failed: %s: %v", agent.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := a.AcpAgents.Save(&updated); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.AcpProbeResult{Agent: a.acpAgentDTO(&updated), OK: true}, nil
}

func (a *App) handleAcpAgentsAuthenticate(req contracts.AcpAuthenticateRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := a.acpReady(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if strings.TrimSpace(req.MethodID) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "method_id is required"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := rt.Authenticate(ctx, agent, req.MethodID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	agent.AuthMethodID = req.MethodID
	agent.UpdatedAt = time.Now().UTC()
	if err := a.AcpAgents.Save(agent); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.AcpProbeResult{Agent: a.acpAgentDTO(agent), OK: true}, nil
}

func (a *App) handleAcpAgentsRefreshCatalog(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := a.acpReady(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updated, err := rt.RefreshCatalog(ctx, agent)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := a.AcpAgents.Save(&updated); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.AcpProbeResult{Agent: a.acpAgentDTO(&updated), OK: true}, nil
}

func (a *App) handleAcpRunsList(req contracts.AcpRunsListRequest) (any, *contracts.RPCError) {
	if a.Acp == nil {
		return contracts.AcpRunsListResult{Runs: []contracts.AcpRunDTO{}}, nil
	}
	runs := a.Acp.List(req.ConversationID)
	out := make([]contracts.AcpRunDTO, 0, len(runs))
	for _, r := range runs {
		out = append(out, acpRunDTO(r))
	}
	return contracts.AcpRunsListResult{Runs: out}, nil
}

func (a *App) handleAcpRunsGet(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	run, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	_ = rt
	return acpRunDTO(run), nil
}

func (a *App) handleAcpRunsSteer(req contracts.AcpRunSteerRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := rt.Steer(req.ID, req.Text); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return acpRunDTO(run), nil
}

func (a *App) handleAcpRunsStop(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := rt.Stop(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return acpRunDTO(run), nil
}

func (a *App) handleAcpRunsWait(req contracts.AcpRunWaitRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	timeout := 15 * time.Minute
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	run, err := rt.Wait(ctx, req.ID)
	if run == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err != nil && ctx.Err() != nil {
		return acpRunDTO(run), nil
	}
	return acpRunDTO(run), nil
}

func (a *App) handleAcpRunsPromote(req contracts.AcpRunPromoteRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	tier := domain.RiskTier(req.Tier)
	if !domain.IsValidRiskTier(tier) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "tier must be read_only, edit_confirmed, or bypass"}
	}
	if err := rt.PromoteRisk(req.ID, tier); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	a.log("info", "acp", "risk tier promoted: run=%s tier=%s", req.ID, tier)
	run, _ := rt.Get(req.ID)
	return acpRunDTO(run), nil
}

func (a *App) handleAcpRunsSetMode(req contracts.AcpRunSetModeRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := a.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.SetMode(ctx, req.ID, req.ModeID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return acpRunDTO(run), nil
}

func (a *App) handleAcpPermissionDecide(req contracts.AcpPermissionDecideRequest) (any, *contracts.RPCError) {
	run, rt, rpcErr := a.acpRun(req.RunID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	outcome := domain.PermissionOutcome(req.Outcome)
	if outcome == "" {
		outcome = domain.PermissionAllowOnce
	}
	a.log("info", "acp", "permission decided before apply: run=%s tool=%s outcome=%s", req.RunID, permissionTitle(run), outcome)
	if err := rt.DecidePermission(req.RunID, req.ID, req.OptionID, outcome); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	a.Bus.Emit(contracts.EventAcpPermissionDecided, contracts.AcpPermissionDecidedEvent{
		RunID: req.RunID, ID: req.ID, Outcome: string(outcome), OptionID: req.OptionID,
	})
	updated, _ := rt.Get(req.RunID)
	return acpRunDTO(updated), nil
}

func permissionTitle(run *domain.AcpRun) string {
	return domain.PermissionTitle(run)
}

func (a *App) acpReady(id string) (*domain.AcpAgent, AcpRuntime, *contracts.RPCError) {
	if a.AcpAgents == nil || a.Acp == nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	agent, err := a.AcpAgents.Get(id)
	if err != nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return agent, a.Acp, nil
}

func (a *App) acpRun(id string) (*domain.AcpRun, AcpRuntime, *contracts.RPCError) {
	if a.Acp == nil {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	run, ok := a.Acp.Get(id)
	if !ok {
		return nil, nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "acp run not found"}
	}
	return run, a.Acp, nil
}

func (a *App) enabledAcpAgents() []*domain.AcpAgent {
	if a.AcpAgents == nil {
		return nil
	}
	var out []*domain.AcpAgent
	for _, agent := range a.AcpAgents.List() {
		if agent.Enabled {
			out = append(out, agent)
		}
	}
	return out
}

func (a *App) defaultAcpAgent() *domain.AcpAgent {
	list := a.enabledAcpAgents()
	if len(list) == 0 {
		return nil
	}
	return list[0]
}

func (a *App) spawnAcpSubagents(ctx context.Context, argsJSON []byte) (string, error) {
	if a.Acp == nil || a.AcpAgents == nil {
		return "", fmt.Errorf("ACP subagents are not configured")
	}
	var args struct {
		Prompt    string `json:"prompt"`
		AgentID   string `json:"agent_id"`
		Workspace string `json:"workspace"`
		ModeID    string `json:"mode_id"`
		ModelID   string `json:"model_id"`
		Async     bool   `json:"async"`
		Count     int    `json:"count"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	count := args.Count
	if count <= 0 {
		count = 1
	}
	if count > domain.MaxAcpSpawnCount {
		return "", fmt.Errorf("count must be between 1 and %d", domain.MaxAcpSpawnCount)
	}
	var agent *domain.AcpAgent
	var err error
	if strings.TrimSpace(args.AgentID) != "" {
		agent, err = a.AcpAgents.Get(args.AgentID)
		if err != nil {
			return "", fmt.Errorf("unknown ACP agent %q", args.AgentID)
		}
	} else {
		agent = a.defaultAcpAgent()
		if agent == nil {
			return "", fmt.Errorf("no enabled ACP agents; add one in Providers")
		}
	}
	if !agent.Enabled {
		return "", fmt.Errorf("ACP agent %q is disabled", agent.Name)
	}
	workspace := strings.TrimSpace(args.Workspace)
	if workspace == "" {
		if cid := ConversationIDFromContext(ctx); cid != "" && a.Conversations != nil {
			if conv, err := a.Conversations.Get(cid); err == nil {
				workspace = conv.Workspace
			}
		}
	}
	if workspace == "" {
		workspace = agent.DefaultWorkspace
	}
	if workspace != "" && !domain.PathRooted(workspace) {
		return "", fmt.Errorf("workspace must be an absolute path")
	}

	results := make([]domain.AcpSpawned, count)
	for i := 0; i < count; i++ {
		run, err := a.Acp.Spawn(ctx, AcpSpawnRequest{
			Agent:            agent,
			ConversationID:   ConversationIDFromContext(ctx),
			ParentToolCallID: ToolCallIDFromContext(ctx),
			Prompt:           args.Prompt,
			Workspace:        workspace,
			ModeID:           args.ModeID,
			ModelID:          args.ModelID,
		})
		results[i] = domain.AcpSpawned{Run: run, Err: err}
		if err == nil && run != nil {
			a.emitAcpRun(contracts.EventAcpRunStarted, run)
			a.trackPendingSubagent(run.ConversationID, run.ID)
		}
	}
	// Always async: the parent agent gets "starting" immediately and is
	// free to continue other work. When the subagent finishes, the OnDone
	// callback updates the original tool call with the summary and
	// triggers a new turn (tool injection) so the parent agent processes
	// the result without blocking.
	return domain.FormatSpawnResult(results), nil
}

func transcriptSummary(run *domain.AcpRun) string {
	var b strings.Builder
	for _, c := range run.Transcript {
		if c.Kind == "text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		if run.Error != "" {
			return run.Error
		}
		return run.StopReason
	}
	if len(s) > 4000 {
		return s[:4000] + "…"
	}
	return s
}

func isAbsPath(p string) bool {
	return len(p) > 0 && (p[0] == '/' || (len(p) > 1 && p[1] == ':'))
}

// acpTranscriptPath returns the absolute path to the JSONL file where
// completed ACP run records are persisted, or "" if storage is not
// configured or not file-backed. Included in the tool result YAML header
// so the parent agent can reference it for debugging.
func acpTranscriptPath(storage domain.AcpRunStorage) string {
	if storage == nil {
		return ""
	}
	type pather interface{ TranscriptPath() string }
	if p, ok := storage.(pather); ok {
		return p.TranscriptPath()
	}
	return ""
}

// subagentCompletionResult builds the tool result: YAML frontmatter
// (status, workspace, output_path) + markdown body (the summary).
func subagentCompletionResult(run *domain.AcpRun, transcriptPath string) string {
	body := subagentCompletionBody(run)
	var y strings.Builder
	y.WriteString("---\n")
	y.WriteString("status: ")
	y.WriteString(yamlScalar(string(run.Status)))
	y.WriteString("\n")
	if run.Workspace != "" {
		y.WriteString("workspace: ")
		y.WriteString(yamlScalar(run.Workspace))
		y.WriteString("\n")
	}
	if transcriptPath != "" {
		y.WriteString("output_path: ")
		y.WriteString(yamlScalar(transcriptPath))
		y.WriteString("\n")
	}
	y.WriteString("---\n\n")
	y.WriteString(body)
	return y.String()
}

// yamlScalar quotes a string for YAML if it contains characters that
// require quoting (colons, quotes, newlines, leading spaces). Otherwise
// returns it bare. This is a minimal escaper — not a full YAML
// serializer — sufficient for the flat key:value header we produce.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":\"'\n#") || s[0] == ' ' || s[0] == '-' {
		return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return s
}

// subagentCompletionBody builds the markdown body of the tool result —
// the human-readable summary the parent agent reads and acts on. Handles
// every completion edge case:
//
//   - Normal (text output): the concatenated text chunks, truncated.
//   - Failed with text: text + error suffix so the parent agent sees
//     both the partial work and the failure reason.
//   - Tool-only / empty / thinking-only: no text chunks → fall back to
//     a structured summary that includes stop_reason, error, and the
//     last tool call (if any) so the parent agent has enough context to
//     decide what to do next.
//   - Cancelled: explicit cancellation message.
func subagentCompletionBody(run *domain.AcpRun) string {
	var b strings.Builder
	for _, c := range run.Transcript {
		if c.Kind == "text" && c.Text != "" {
			b.WriteString(c.Text)
		}
	}
	textOut := strings.TrimSpace(b.String())

	isFailed := run.Status == domain.AcpRunFailed
	isCancelled := run.Status == domain.AcpRunCancelled

	// Cancelled: explicit message regardless of partial text.
	if isCancelled {
		if textOut != "" {
			return textOut + "\n\n[Subagent was cancelled.]"
		}
		return "Subagent was cancelled."
	}

	// Failed with text: include the error so the parent agent sees both.
	if isFailed && textOut != "" {
		errPart := run.Error
		if errPart == "" {
			errPart = run.StopReason
		}
		if errPart != "" {
			if len(textOut) > 3800 {
				textOut = textOut[:3800] + "…"
			}
			return textOut + "\n\n[Subagent failed: " + errPart + "]"
		}
	}

	// Normal text output.
	if textOut != "" {
		if len(textOut) > 4000 {
			return textOut[:4000] + "…"
		}
		return textOut
	}

	// No text output (tool-only, thinking-only, or empty). Build a
	// structured fallback so the parent agent has enough context.
	return structuredFallbackSummary(run)
}

// structuredFallbackSummary builds a summary when the subagent produced
// no text chunks (tool-only, thinking-only, or empty output). It
// includes the stop reason, error, and the last tool call so the parent
// agent can decide what to do next.
func structuredFallbackSummary(run *domain.AcpRun) string {
	var parts []string

	// Status line.
	switch run.Status {
	case domain.AcpRunFailed:
		parts = append(parts, "Subagent failed.")
	case domain.AcpRunCancelled:
		parts = append(parts, "Subagent was cancelled.")
	case domain.AcpRunCompleted:
		parts = append(parts, "Subagent completed with no text output.")
	default:
		parts = append(parts, "Subagent ended (status: "+string(run.Status)+").")
	}

	// Error reason (if any).
	if run.Error != "" {
		parts = append(parts, "Error: "+run.Error)
	}

	// Stop reason (if any and different from error).
	if run.StopReason != "" && run.StopReason != run.Error {
		parts = append(parts, "Stop reason: "+run.StopReason)
	}

	// Last tool call (if any) — gives the parent agent context about
	// what the subagent was doing.
	lastTool := lastToolCallFromTranscript(run)
	if lastTool != "" {
		parts = append(parts, "Last tool: "+lastTool)
	}

	return strings.Join(parts, " ")
}

// lastToolCallFromTranscript scans the transcript in reverse and returns
// a compact description of the last tool call (title + status), or empty
// if none. AcpTranscriptChunk uses flat fields (ToolTitle, ToolStatus)
// rather than a nested ToolCall pointer.
func lastToolCallFromTranscript(run *domain.AcpRun) string {
	for i := len(run.Transcript) - 1; i >= 0; i-- {
		c := run.Transcript[i]
		if c.Kind == "tool" && c.ToolTitle != "" {
			s := c.ToolTitle
			if c.ToolStatus != "" {
				s += " (" + c.ToolStatus + ")"
			}
			return s
		}
	}
	return ""
}

func (a *App) SpawnSubagents(ctx context.Context, argsJSON []byte) (string, error) {
	return a.spawnAcpSubagents(ctx, argsJSON)
}

func (a *App) SteerAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	res, rpcErr := a.handleAcpRunsSteer(contracts.AcpRunSteerRequest{ID: args.ID, Text: args.Text})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func (a *App) StopAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	res, rpcErr := a.handleAcpRunsStop(contracts.AcpRunIDRequest{ID: args.ID})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func (a *App) WaitAcpRun(ctx context.Context, argsJSON []byte) (string, error) {
	var args struct {
		ID        string `json:"id"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	res, rpcErr := a.handleAcpRunsWait(contracts.AcpRunWaitRequest{ID: args.ID, TimeoutMS: args.TimeoutMS})
	if rpcErr != nil {
		return "", fmt.Errorf("%s", rpcErr.Message)
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func (a *App) EnabledAcpAgents() []*domain.AcpAgent {
	return a.enabledAcpAgents()
}

func (a *App) emitAcpRun(event string, run *domain.AcpRun) {
	if run == nil {
		return
	}
	a.Bus.Emit(event, contracts.AcpRunEvent{Run: acpRunDTO(run)})
}

func (a *App) wireAcpCallbacks() {
	sink, ok := a.Acp.(interface {
		SetCallbacks(
			onUpdate, onDone func(*domain.AcpRun),
			onPerm func(*domain.AcpRun, domain.AcpPermissionRequest),
			onMode func(*domain.AcpRun, string),
		)
	})
	if !ok {
		return
	}
	sink.SetCallbacks(
		func(run *domain.AcpRun) { a.emitAcpRun(contracts.EventAcpRunUpdated, run) },
		func(run *domain.AcpRun) {
			a.emitAcpRun(contracts.EventAcpRunDone, run)
			a.onAcpRunDone(run)
		},
		func(run *domain.AcpRun, req domain.AcpPermissionRequest) {
			a.emitAcpRun(contracts.EventAcpRunUpdated, run)
			perm := contracts.AcpPermissionDTO{
				ID: req.ID, SessionID: req.SessionID, ToolTitle: req.ToolTitle, ToolKind: req.ToolKind,
				Paths: req.Paths, PathCount: len(req.Paths),
			}
			if !req.RequestedAt.IsZero() {
				perm.RequestedAt = req.RequestedAt.Format(timeRFC3339)
			}
			for _, o := range req.Options {
				perm.Options = append(perm.Options, contracts.AcpPermissionOptionDTO{ID: o.ID, Name: o.Name, Kind: o.Kind})
			}
			a.Bus.Emit(contracts.EventAcpPermissionRequested, contracts.AcpPermissionEvent{RunID: run.ID, Permission: perm})
		},
		func(run *domain.AcpRun, source string) {
			a.Bus.Emit(contracts.EventAcpSessionModeChanged, contracts.AcpModeChangedEvent{
				RunID: run.ID, ModeID: run.CurrentModeID, Source: source,
			})
			a.emitAcpRun(contracts.EventAcpRunUpdated, run)
		},
	)
}

// onAcpRunDone handles subagent completion: persists the transcript to
// JSONL storage, updates the original `subagent` tool call from running
// to ok/fail with a text summary, and triggers a new parent-agent turn
// (tool injection) so the parent processes the result without blocking.
//
// This is the async completion path. The spawn path returns immediately
// with status "starting"; this callback fires when the subagent finishes
// (completed, failed, or cancelled) and closes the loop.
func (a *App) onAcpRunDone(run *domain.AcpRun) {
	if run == nil || run.ConversationID == "" {
		return
	}

	// 1. Persist the full transcript to JSONL storage.
	if a.AcpRunStorage != nil {
		record := domain.AcpRunRecord{
			ID:               run.ID,
			AgentID:          run.AgentID,
			AgentName:        run.AgentName,
			ConversationID:   run.ConversationID,
			ParentToolCallID: run.ParentToolCallID,
			Workspace:        run.Workspace,
			Prompt:           run.Prompt,
			Status:           run.Status,
			ModelID:          run.CurrentModelID,
			RiskTier:         run.RiskTier,
			StopReason:       run.StopReason,
			Error:            run.Error,
			Transcript:       run.Transcript,
			StartedAt:        run.StartedAt,
			EndedAt:          run.EndedAt,
		}
		if err := a.AcpRunStorage.Save(record); err != nil {
			a.log("error", "acp", "failed to persist run %s: %v", run.ID, err)
		}
	}

	// 2. Untrack the pending subagent.
	wasPending := a.untrackPendingSubagent(run.ConversationID, run.ID)

	// 3. Update the original `subagent` tool call with the structured
	// result (YAML frontmatter + markdown body). The tool call was marked
	// "running" when spawned; now it transitions to ok/fail.
	if run.ParentToolCallID != "" {
		transcriptPath := acpTranscriptPath(a.AcpRunStorage)
		result := domain.SubagentCompletionResult(run, transcriptPath)
		status := domain.ToolOK
		if run.Status == domain.AcpRunFailed || run.Status == domain.AcpRunCancelled {
			status = domain.ToolFailed
		}
		a.updateSubagentToolCall(run.ConversationID, run.ParentToolCallID, status, result)
	}

	// 4. Trigger a new parent-agent turn (tool injection). Only trigger
	// if this run was pending (not already completed) and no turn is
	// currently active for the conversation — the agent loop will pick
	// up the updated tool call and process the result.
	if wasPending {
		a.triggerSubagentCompletionTurn(run.ConversationID)
	}
}

// updateSubagentToolCall finds the original `subagent` tool call by its
// ID and updates its status + output in the conversation store. Emits a
// ToolCompleted event so the frontend re-renders the delegation card.
func (a *App) updateSubagentToolCall(conversationID, toolCallID string, status domain.ToolCallStatus, output string) {
	conv, err := a.Conversations.Get(conversationID)
	if err != nil {
		a.log("error", "acp", "updateSubagentToolCall: conversation %s not found: %v", conversationID, err)
		return
	}
	conv = a.updateToolResult(conv, "", toolCallID, status, output, nil)
	if err := a.Conversations.Save(conv); err != nil {
		a.log("error", "acp", "updateSubagentToolCall: save failed: %v", err)
		return
	}
	a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
		ConversationID: conversationID,
		ToolCallID:     toolCallID,
		Name:           "subagent",
		Status:         string(status),
		Output:         output,
	})
}

// triggerSubagentCompletionTurn starts a new agent turn for the
// conversation to process the completed subagent's output. This is the
// "tool injection" mechanism: the parent agent sees the updated tool
// call in its message history and processes the result as if it had
// just completed the tool call itself.
//
// The turn is only started if the conversation is idle (no active turn).
// If a turn is already running, the updated tool call will be visible
// in the next round's context and processed naturally.
func (a *App) triggerSubagentCompletionTurn(conversationID string) {
	a.startMu.Lock()
	defer a.startMu.Unlock()

	// If a turn is already active, the updated tool call will be picked
	// up in the next round — no need to start a new one.
	if a.activeRunForConversation(conversationID) != nil {
		return
	}

	conv, err := a.Conversations.Get(conversationID)
	if err != nil {
		a.log("error", "acp", "triggerSubagentCompletionTurn: conversation %s not found: %v", conversationID, err)
		return
	}
	if conv.Status != "idle" {
		return
	}

	// Find the provider + model from the conversation's last assistant
	// message. If none, use the first enabled provider.
	provider, model, apiKey, effort, err := a.resolveConversationProvider(conv)
	if err != nil {
		a.log("error", "acp", "triggerSubagentCompletionTurn: no provider: %v", err)
		return
	}

	now := time.Now().UTC()
	asstMsg := domain.Message{
		ID:         domain.NewID("msg"),
		Role:       domain.RoleAssistant,
		CreatedAt:  now,
		ProviderID: provider.ID,
	}
	conv.AddMessage(asstMsg)
	conv.Status = "running"
	if err := a.Conversations.Save(conv); err != nil {
		a.log("error", "acp", "triggerSubagentCompletionTurn: save failed: %v", err)
		return
	}

	turnCtx, cancel := context.WithCancel(context.Background())
	run := &TurnRun{
		ID:             domain.NewID("run"),
		ConversationID: conv.ID,
		MessageID:      asstMsg.ID,
		Ctx:            turnCtx,
		Cancel:         cancel,
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	bareModel := strings.TrimSpace(strings.TrimPrefix(model, provider.ID+"/"))
	caps := modelCapabilities(provider, bareModel)

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, effort, asstMsg.ID, false, caps, "A subagent completed. Process its output and continue.")
	})
	a.log("info", "acp", "subagent completion turn triggered for %s (model %s)", conv.ID, bareModel)
}

// resolveConversationProvider picks the provider + model + API key +
// effort for a subagent completion turn. It uses the conversation's
// last successful assistant message model; if none, it falls back to
// the first enabled provider with a model.
func (a *App) resolveConversationProvider(conv *domain.Conversation) (*domain.Provider, string, string, string, error) {
	model := ""
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		m := conv.Messages[i]
		if m.Role == domain.RoleAssistant && m.Model != "" && m.Status == domain.StatusDone {
			model = m.Model
			break
		}
	}
	if model == "" {
		model = conv.Model
	}
	if model != "" {
		p, bare, key, rpcErr := a.resolveModel(model)
		if rpcErr == nil && p != nil && p.Enabled {
			return p, bare, key, conv.Effort, nil
		}
	}
	// Fallback: first enabled provider with at least one model.
	for _, p := range a.Providers.List() {
		if !p.Enabled {
			continue
		}
		models := p.Models
		if len(models) == 0 {
			continue
		}
		key, has, err := a.Credentials.Get(p.ID)
		if err != nil || !has || key == "" {
			continue
		}
		return p, models[0].ID, key, conv.Effort, nil
	}
	return nil, "", "", "", fmt.Errorf("no enabled provider with a model")
}

func availableAcpSummary(agents []*domain.AcpAgent) (list, def string) {
	return domain.AvailableAcpSummary(agents)
}
