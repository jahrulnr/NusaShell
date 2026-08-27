package application

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"

	"gopkg.in/yaml.v3"
)

// dispatchAcp routes acp.* RPC methods (agents, runs, permission) to their
// handlers. Called by App.Dispatch for any method whose first segment is
// "acp".
func (a *App) dispatchAcp(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	switch method {
	case contracts.MethodAcpAgentsList:
		return a.handleAcpAgentsList()
	case contracts.MethodAcpAgentsSave:
		var req contracts.AcpAgentSaveRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsSave(req)
	case contracts.MethodAcpAgentsDelete:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsDelete(req)
	case contracts.MethodAcpAgentsProbe:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsProbe(req)
	case contracts.MethodAcpAgentsAuthenticate:
		var req contracts.AcpAuthenticateRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsAuthenticate(req)
	case contracts.MethodAcpAgentsRefreshCatalog:
		var req contracts.AcpAgentIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpAgentsRefreshCatalog(req)
	case contracts.MethodAcpRunsList:
		var req contracts.AcpRunsListRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsList(req)
	case contracts.MethodAcpRunsGet:
		var req contracts.AcpRunIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsGet(req)
	case contracts.MethodAcpRunsSteer:
		var req contracts.AcpRunSteerRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsSteer(req)
	case contracts.MethodAcpRunsStop:
		var req contracts.AcpRunIDRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsStop(req)
	case contracts.MethodAcpRunsWait:
		var req contracts.AcpRunWaitRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsWait(req)
	case contracts.MethodAcpRunsPromote:
		var req contracts.AcpRunPromoteRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsPromote(req)
	case contracts.MethodAcpRunsSetMode:
		var req contracts.AcpRunSetModeRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpRunsSetMode(req)
	case contracts.MethodAcpPermissionDecide:
		var req contracts.AcpPermissionDecideRequest
		if rpcErr := contracts.DecodePayload(payload, &req); rpcErr != nil {
			return nil, rpcErr
		}
		return a.handleAcpPermissionDecide(req)
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown acp method: " + method}
	}
}

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
	// Live runtime runs first; persisted records fill in settled runs so
	// the UI can reopen a completed subagent's transcript from the drawer
	// even after it left the dock (or after a restart).
	seen := make(map[string]bool)
	out := make([]contracts.AcpRunDTO, 0, 8)
	for _, r := range a.Acp.List(req.ConversationID) {
		seen[r.ID] = true
		out = append(out, acpRunDTO(r))
	}
	if a.AcpRunStorage != nil {
		for _, rec := range a.AcpRunStorage.List(req.ConversationID) {
			if seen[rec.ID] {
				continue
			}
			seen[rec.ID] = true
			out = append(out, acpRunDTO(acpRunFromRecord(rec)))
		}
	}
	return contracts.AcpRunsListResult{Runs: out}, nil
}

// acpRunFromRecord rebuilds an in-memory AcpRun from a persisted record
// so settled runs can be listed and served after the runtime forgets them.
func acpRunFromRecord(rec domain.AcpRunRecord) *domain.AcpRun {
	run := &domain.AcpRun{
		ID:               rec.ID,
		AgentID:          rec.AgentID,
		AgentName:        rec.AgentName,
		ConversationID:   rec.ConversationID,
		ParentToolCallID: rec.ParentToolCallID,
		Workspace:        rec.Workspace,
		Prompt:           rec.Prompt,
		Status:           rec.Status,
		CurrentModelID:   rec.ModelID,
		RiskTier:         rec.RiskTier,
		StopReason:       rec.StopReason,
		Error:            rec.Error,
		Transcript:       rec.Transcript,
		StartedAt:        rec.StartedAt,
		EndedAt:          rec.EndedAt,
		UpdatedAt:        rec.EndedAt,
	}
	return run
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
	a.log("info", "acp", "permission decided before apply: run=%s tool=%s outcome=%s", req.RunID, domain.PermissionTitle(run), outcome)
	if err := rt.DecidePermission(req.RunID, req.ID, req.OptionID, outcome); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	a.Bus.Emit(contracts.EventAcpPermissionDecided, contracts.AcpPermissionDecidedEvent{
		RunID: req.RunID, ID: req.ID, Outcome: string(outcome), OptionID: req.OptionID,
	})
	updated, _ := rt.Get(req.RunID)
	return acpRunDTO(updated), nil
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

// SpawnSubagents is the `subagent` tool implementation. It spawns one or
// more ACP subagents and always returns "starting" immediately: the parent
// agent is free to continue other work, and when a subagent finishes the
// OnDone callback updates the original tool call with the summary and
// triggers a new turn (tool injection) so the parent processes the result
// without blocking.
func (a *App) SpawnSubagents(ctx context.Context, argsJSON []byte) (string, error) {
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
		list := a.enabledAcpAgents()
		if len(list) == 0 {
			return "", fmt.Errorf("no enabled ACP agents; add one in Providers")
		}
		agent = list[0]
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

	// Parent plan handoff: ACP subagents do not receive the parent
	// conversation, so a running task's plan must travel explicitly. When
	// the parent conversation has a todo brief, point the subagent at the
	// mirrored plan file (read-first); when the file is missing or the
	// subagent runs in a different workspace (sandbox may refuse paths
	// outside it), inline a compact Objective + Done when summary instead
	// of relying on the subagent's memory of a plan it never saw.
	prompt := args.Prompt
	if a.Todos != nil {
		if cid := ConversationIDFromContext(ctx); cid != "" {
			if brief := a.Todos.GetBrief(cid); strings.TrimSpace(brief) != "" {
				prompt = withParentPlan(prompt, a.Todos.PlanPath(cid), brief, workspace)
			}
		}
	}

	results := make([]domain.AcpSpawned, count)
	for i := 0; i < count; i++ {
		run, err := a.Acp.Spawn(ctx, AcpSpawnRequest{
			Agent:            agent,
			ConversationID:   ConversationIDFromContext(ctx),
			ParentToolCallID: ToolCallIDFromContext(ctx),
			Prompt:           prompt,
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

// withParentPlan appends the parent conversation's plan context to a
// subagent prompt. planPath is the mirrored plan file ("" when unresolvable);
// brief is the full brief body. When the plan file exists on disk, the
// subagent is told to read it first. When the file is missing or the
// subagent workspace differs from the plan file's location (a sandboxed
// agent may refuse paths outside its workspace), a compact Objective +
// Done when summary is inlined instead so the plan intent always travels.
func withParentPlan(prompt, planPath, brief, workspace string) string {
	var sb strings.Builder
	sb.WriteString(prompt)
	sb.WriteString("\n\n")
	if planPath != "" {
		if _, err := os.Stat(planPath); err == nil {
			sb.WriteString("Parent plan file (read this first): " + planPath + "\n")
			if workspace != "" && !strings.HasPrefix(planPath, workspace+string(os.PathSeparator)) {
				// The plan file lives outside the subagent workspace;
				// include a summary as a fallback in case the sandbox
				// refuses to read it.
				if summary := domain.SummarizeBrief(brief); summary != "" {
					sb.WriteString("\nParent plan summary (in case the file is unreadable):\n" + summary + "\n")
				}
			}
			return sb.String()
		}
	}
	if summary := domain.SummarizeBrief(brief); summary != "" {
		sb.WriteString("Parent plan summary:\n" + summary + "\n")
	}
	return sb.String()
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
	b, _ := yaml.Marshal(res)
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---", nil
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
	b, _ := yaml.Marshal(res)
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---", nil
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
	b, _ := yaml.Marshal(res)
	return "---\n" + strings.TrimRight(string(b), "\n") + "\n---", nil
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

// onAcpRunDone handles subagent completion: persists the transcript to
// JSONL storage, updates the original `subagent` tool call from running
// to ok/fail with a brief terminal status, injects a synthetic
// `subagent_result` tool call carrying the full result, and triggers a
// new parent-agent turn (tool injection) so the parent processes the
// result without blocking.
//
// This is the async completion path. The spawn path returns immediately
// with status "starting"; this callback fires when the subagent finishes
// (completed, failed, or cancelled) and closes the loop.
func (a *App) onAcpRunDone(run *domain.AcpRun) {
	if run == nil || run.ConversationID == "" {
		return
	}

	// 1. Persist the full transcript to JSONL storage.
	outputPath := ""
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
		} else {
			outputPath = a.AcpRunStorage.Path(run.ConversationID, run.ID)
		}
	}

	// 2. Untrack the pending subagent.
	wasPending := a.untrackPendingSubagent(run.ConversationID, run.ID)

	// 3. Update the original `subagent` tool call to a brief terminal
	// status (the full result travels in the synthetic subagent_result
	// tool call below, so old history is not silently rewritten) and
	// inject the synthetic subagent_result message carrying the full
	// result. The tool call was marked "running" when spawned; now it
	// transitions to ok/fail.
	if run.ParentToolCallID != "" {
		status := domain.ToolOK
		if run.Status == domain.AcpRunFailed || run.Status == domain.AcpRunCancelled {
			status = domain.ToolFailed
		}
		a.completeSubagentRun(run.ConversationID, run.ParentToolCallID, status, run, outputPath)
	}

	// 4. Trigger a new parent-agent turn (tool injection). Only trigger
	// if this run was pending (not already completed) and no turn is
	// currently active for the conversation — the agent loop will pick
	// up the synthetic subagent_result tool call and process the result.
	if wasPending {
		a.triggerSubagentCompletionTurn(run.ConversationID)
	}
}

// completeSubagentRun updates the original `subagent` tool call to its
// brief terminal state and injects a synthetic assistant message carrying
// the `subagent_result` tool call with the full result pre-filled
// (announcement-style). Persisted so the model sees it in this turn and
// in later turns (auto-continue), and the UI renders it as a normal tool
// card. Keeps the cache-stable system prompt untouched.
func (a *App) completeSubagentRun(conversationID, toolCallID string, status domain.ToolCallStatus, run *domain.AcpRun, outputPath string) {
	conv, err := a.Conversations.Get(conversationID)
	if err != nil {
		a.log("error", "acp", "completeSubagentRun: conversation %s not found: %v", conversationID, err)
		return
	}
	conv = a.updateToolResult(conv, "", toolCallID, status, domain.SubagentBriefResult(run), nil)
	conv.AddMessage(a.subagentResultMessage(run, outputPath, status))
	if err := a.Conversations.Save(conv); err != nil {
		a.log("error", "acp", "completeSubagentRun: save failed: %v", err)
		return
	}
	a.Bus.Emit(contracts.EventToolCompleted, contracts.ToolCompletedEvent{
		ConversationID: conversationID,
		ToolCallID:     toolCallID,
		Name:           "subagent",
		Status:         string(status),
		Output:         domain.SubagentBriefResult(run),
	})
}

// subagentResultMessage builds the synthetic assistant message carrying
// the `subagent_result` tool call with its result pre-filled. Mirrors
// restartAnnouncement: persisted into the conversation so the model
// processes the result like any freshly completed tool output.
func (a *App) subagentResultMessage(run *domain.AcpRun, outputPath string, status domain.ToolCallStatus) domain.Message {
	return domain.Message{
		ID:        domain.NewID("msg"),
		Role:      domain.RoleAssistant,
		CreatedAt: time.Now().UTC(),
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{
			ID:     domain.SubagentResultPrefix + randomNonce(),
			Name:   domain.SubagentResultToolName,
			Args:   domain.SubagentResultArgs(run.ID),
			Status: status,
			Output: domain.SubagentCompletionResult(run, outputPath),
		}},
	}
}

// triggerSubagentCompletionTurn starts a new agent turn for the
// conversation to process the completed subagent's output. This is the
// "tool injection" mechanism: the parent agent sees the synthetic
// subagent_result tool call in its message history and processes the
// result as if it had just completed the tool call itself.
//
// The turn is only started if the conversation is idle (no active turn).
// If a turn is already running, the synthetic subagent_result message is
// already persisted, so the next round's context picks it up naturally.
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
		Workspace:      conv.Workspace,
	}
	a.runsMu.Lock()
	a.runs[run.ID] = run
	a.runsMu.Unlock()

	bareModel := strings.TrimSpace(strings.TrimPrefix(model, provider.ID+"/"))
	caps := modelCapabilitiesWithLearned(provider, bareModel, a.learnedParams, a.modelOverrides)

	a.goSafe("agent", func() {
		a.runTurn(run, provider, apiKey, bareModel, effort, asstMsg.ID, false, caps)
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
