package subagent

import (
	"encoding/json"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// Dispatch routes acp.* RPC methods to their handlers.
func (s *Service) Dispatch(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if s == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "ACP runtime is not available"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodAcpAgentsList:           rpcdispatch.NoPayload(s.HandleAgentsList),
		contracts.MethodAcpAgentsSave:           rpcdispatch.DecodeReq(s.HandleAgentsSave),
		contracts.MethodAcpAgentsDelete:         rpcdispatch.DecodeReq(s.HandleAgentsDelete),
		contracts.MethodAcpAgentsProbe:          rpcdispatch.DecodeReq(s.HandleAgentsProbe),
		contracts.MethodAcpAgentsAuthenticate:   rpcdispatch.DecodeReq(s.HandleAgentsAuthenticate),
		contracts.MethodAcpAgentsRefreshCatalog: rpcdispatch.DecodeReq(s.HandleAgentsRefreshCatalog),
		contracts.MethodAcpRunsList:             rpcdispatch.DecodeReq(s.HandleRunsList),
		contracts.MethodAcpRunsGet:              rpcdispatch.DecodeReq(s.HandleRunsGet),
		contracts.MethodAcpRunsSteer:            rpcdispatch.DecodeReq(s.HandleRunsSteer),
		contracts.MethodAcpRunsStop:             rpcdispatch.DecodeReq(s.HandleRunsStop),
		contracts.MethodAcpRunsWait:             rpcdispatch.DecodeReq(s.HandleRunsWait),
		contracts.MethodAcpRunsPromote:          rpcdispatch.DecodeReq(s.HandleRunsPromote),
		contracts.MethodAcpRunsSetMode:          rpcdispatch.DecodeReq(s.HandleRunsSetMode),
		contracts.MethodAcpPermissionDecide:     rpcdispatch.DecodeReq(s.HandlePermissionDecide),
	}, "acp")(method, payload)
}

func agentDTO(agent *domain.AcpAgent) contracts.AcpAgentDTO {
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

func runDTO(run *domain.AcpRun) contracts.AcpRunDTO {
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
