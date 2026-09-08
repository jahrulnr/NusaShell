package subagent

import (
	"context"
	"errors"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

func (s *Service) HandleAgentsList() (any, *contracts.RPCError) {
	if s.deps.Agents == nil {
		return contracts.AcpAgentsListResult{Agents: []contracts.AcpAgentDTO{}}, nil
	}
	list := s.deps.Agents.List()
	out := make([]contracts.AcpAgentDTO, 0, len(list))
	for _, agent := range list {
		out = append(out, agentDTO(agent))
	}
	return contracts.AcpAgentsListResult{Agents: out}, nil
}

func (s *Service) HandleAgentsSave(req contracts.AcpAgentSaveRequest) (any, *contracts.RPCError) {
	if s.deps.Agents == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP agent store is not available"}
	}
	if msg := domain.ValidateAcpAgentSave(req.Name, req.Command); msg != "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: msg}
	}
	var agent *domain.AcpAgent
	if req.ID != "" {
		existing, err := s.deps.Agents.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		if existing.Command != "" && existing.Command != strings.TrimSpace(req.Command) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "command is immutable after registration; delete and recreate the agent to change it"}
		}
		agent = existing
	} else {
		agent = &domain.AcpAgent{ID: domain.NewID(domain.IDPrefixAcpAgent), Enabled: true}
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
	agent.UpdatedAt = clock.NewTime().Time()
	if err := s.deps.Agents.Save(agent); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.log("info", "acp", "acp agent saved: %s", agent.Name)
	// The subagent tool description is global: every conversation's cached
	// tool block is invalidated, so every active agent is told.
	s.notifyAgentsChanged()
	return contracts.AcpAgentsListResult{Agents: []contracts.AcpAgentDTO{agentDTO(agent)}}, nil
}

func (s *Service) HandleAgentsDelete(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	if s.deps.Agents == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP agent store is not available"}
	}
	if _, err := s.deps.Agents.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := s.deps.Agents.Delete(req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.log("info", "acp", "acp agent deleted: %s", req.ID)
	s.notifyAgentsChanged()
	return map[string]bool{"ok": true}, nil
}

func (s *Service) HandleAgentsProbe(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := s.acpReady(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	updated, err := rt.Probe(ctx, agent)
	if err != nil {
		s.log("warn", "acp", "probe failed: %s: %v", agent.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	updated.UpdatedAt = clock.NewTime().Time()
	if err := s.deps.Agents.Save(&updated); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return contracts.AcpProbeResult{Agent: agentDTO(&updated), OK: true}, nil
}

func (s *Service) HandleAgentsAuthenticate(req contracts.AcpAuthenticateRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := s.acpReady(req.ID)
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
	agent.UpdatedAt = clock.NewTime().Time()
	if err := s.deps.Agents.Save(agent); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return contracts.AcpProbeResult{Agent: agentDTO(agent), OK: true}, nil
}

func (s *Service) HandleAgentsRefreshCatalog(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	agent, rt, rpcErr := s.acpReady(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updated, err := rt.RefreshCatalog(ctx, agent)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	updated.UpdatedAt = clock.NewTime().Time()
	if err := s.deps.Agents.Save(&updated); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return contracts.AcpProbeResult{Agent: agentDTO(&updated), OK: true}, nil
}

func (s *Service) HandleRunsList(req contracts.AcpRunsListRequest) (any, *contracts.RPCError) {
	// Live runtime runs first; persisted records fill in settled runs so
	// the UI can reopen a completed subagent's transcript from the drawer
	// even after it left the dock (or after a restart).
	seen := make(map[string]bool)
	out := make([]contracts.AcpRunDTO, 0, 8)
	if s.deps.Runtime != nil {
		for _, r := range s.deps.Runtime.List(req.ConversationID) {
			seen[r.ID] = true
			out = append(out, runDTO(r))
		}
	}
	for _, r := range s.delegates.List(req.ConversationID) {
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		out = append(out, runDTO(r))
	}
	if s.deps.RunStorage != nil {
		for _, rec := range s.deps.RunStorage.List(req.ConversationID) {
			if seen[rec.ID] {
				continue
			}
			seen[rec.ID] = true
			out = append(out, runDTO(runFromRecord(rec)))
		}
	}
	return contracts.AcpRunsListResult{Runs: out}, nil
}

// runFromRecord rebuilds an in-memory AcpRun from a persisted record
// so settled runs can be listed and served after the runtime forgets them.
func runFromRecord(rec domain.AcpRunRecord) *domain.AcpRun {
	run := &domain.AcpRun{
		TaskState: domain.TaskState[domain.AcpRunStatus]{
			ID:         rec.ID,
			Status:     rec.Status,
			StartedAt:  rec.StartedAt,
			FinishedAt: rec.EndedAt,
			Error:      rec.Error,
		},
		AgentID:          rec.AgentID,
		AgentName:        rec.AgentName,
		Title:            rec.Title,
		ConversationID:   rec.ConversationID,
		ParentToolCallID: rec.ParentToolCallID,
		Workspace:        rec.Workspace,
		Prompt:           rec.Prompt,
		CurrentModelID:   rec.ModelID,
		RiskTier:         rec.RiskTier,
		StopReason:       rec.StopReason,
		Transcript:       rec.Transcript,
		UpdatedAt:        rec.EndedAt,
	}
	return run
}

func (s *Service) HandleRunsGet(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	if s.deps.Runtime != nil {
		if run, ok := s.deps.Runtime.Get(req.ID); ok {
			return runDTO(run), nil
		}
	}
	if run, ok := s.delegates.Get(req.ID); ok {
		return runDTO(run), nil
	}
	// The runtime only owns live runs. Terminal runs survive in the per-room
	// .acp store and must remain readable after a backend restart so a room's
	// persisted subagent card can reopen its transcript.
	if s.deps.RunStorage != nil {
		if record, ok := s.deps.RunStorage.Load(req.ID); ok {
			return runDTO(runFromRecord(record)), nil
		}
	}
	return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "acp run not found"}
}

func (s *Service) HandleRunsSteer(req contracts.AcpRunSteerRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := s.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := rt.Steer(req.ID, req.Text); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return runDTO(run), nil
}

func (s *Service) HandleRunsStop(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := s.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err := rt.Stop(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return runDTO(run), nil
}

func (s *Service) HandleRunsWait(req contracts.AcpRunWaitRequest) (any, *contracts.RPCError) {
	run, rpcErr := s.WaitRun(context.Background(), req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return runDTO(run), nil
}

// WaitRun waits for an ACP run, honouring the parent context for cancellation.
func (s *Service) WaitRun(parent context.Context, req contracts.AcpRunWaitRequest) (*domain.AcpRun, *contracts.RPCError) {
	_, rt, rpcErr := s.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	timeout := 15 * time.Minute
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	run, err := rt.Wait(ctx, req.ID)
	if err != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return nil, rpcdispatch.Internal(parentErr)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return nil, rpcdispatch.Internal(err)
		}
	}
	if run == nil {
		message := "acp run not found"
		if err != nil {
			message = err.Error()
		}
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: message}
	}
	return run, nil
}

func (s *Service) HandleRunsPromote(req contracts.AcpRunPromoteRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := s.acpRun(req.ID)
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
	s.log("info", "acp", "risk tier promoted: run=%s tier=%s", req.ID, tier)
	run, _ := rt.Get(req.ID)
	return runDTO(run), nil
}

func (s *Service) HandleRunsSetMode(req contracts.AcpRunSetModeRequest) (any, *contracts.RPCError) {
	_, rt, rpcErr := s.acpRun(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.SetMode(ctx, req.ID, req.ModeID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	run, _ := rt.Get(req.ID)
	return runDTO(run), nil
}

func (s *Service) HandlePermissionDecide(req contracts.AcpPermissionDecideRequest) (any, *contracts.RPCError) {
	run, rt, rpcErr := s.acpRun(req.RunID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	outcome := domain.PermissionOutcome(req.Outcome)
	if outcome == "" {
		outcome = domain.PermissionAllowOnce
	}
	s.log("info", "acp", "permission decided before apply: run=%s tool=%s outcome=%s", req.RunID, domain.PermissionTitle(run), outcome)
	if err := rt.DecidePermission(req.RunID, req.ID, req.OptionID, outcome); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	s.emit(contracts.EventAcpPermissionDecided, contracts.AcpPermissionDecidedEvent{
		RunID: req.RunID, ID: req.ID, Outcome: string(outcome), OptionID: req.OptionID,
	})
	updated, _ := rt.Get(req.RunID)
	return runDTO(updated), nil
}
