package application

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

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
		dto.UpdatedAt = clock.NewTime(agent.UpdatedAt).Format(timeRFC3339)
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
		dto.StartedAt = clock.NewTime(run.StartedAt).Format(timeRFC3339)
	}
	if !run.UpdatedAt.IsZero() {
		dto.UpdatedAt = clock.NewTime(run.UpdatedAt).Format(timeRFC3339)
	}
	if !run.FinishedAt.IsZero() {
		dto.EndedAt = clock.NewTime(run.FinishedAt).Format(timeRFC3339)
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
			chunk.At = clock.NewTime(c.At).Format(timeRFC3339)
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
			dto.PendingPermission.RequestedAt = clock.NewTime(p.RequestedAt).Format(timeRFC3339)
		}
		for _, o := range p.Options {
			dto.PendingPermission.Options = append(dto.PendingPermission.Options, contracts.AcpPermissionOptionDTO{
				ID: o.ID, Name: o.Name, Kind: o.Kind,
			})
		}
	}
	return dto
}
