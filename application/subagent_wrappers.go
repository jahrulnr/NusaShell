package application

import (
	"context"

	"nusashell/application/subagent"
	"nusashell/contracts"
	"nusashell/domain"
)

type (
	AcpAgentStore         = subagent.AgentStore
	AcpSpawnRequest       = subagent.SpawnRequest
	AcpPermissionDecision = subagent.PermissionDecision
	AcpRuntime            = subagent.Runtime
)

// Leftovers that cannot move into application/subagent without importing
// TurnRun / the conversation repository (agent step):
//   deliverRunDone, completeSubagentRunLocked,
//   subagentResultMessage, triggerBackgroundCompletionTurn,
//   resolveConversationProvider, pendingRunDone (turn_run.go).
// Service calls DeliverRunDone / CompleteSubagent injected from App.

func (a *App) handleAcpAgentsList() (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsList()
}

func (a *App) handleAcpAgentsSave(req contracts.AcpAgentSaveRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsSave(req)
}

func (a *App) handleAcpAgentsDelete(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsDelete(req)
}

func (a *App) handleAcpAgentsProbe(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsProbe(req)
}

func (a *App) handleAcpAgentsAuthenticate(req contracts.AcpAuthenticateRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsAuthenticate(req)
}

func (a *App) handleAcpAgentsRefreshCatalog(req contracts.AcpAgentIDRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleAgentsRefreshCatalog(req)
}

func (a *App) handleAcpRunsList(req contracts.AcpRunsListRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsList(req)
}

func (a *App) handleAcpRunsGet(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsGet(req)
}

func (a *App) handleAcpRunsSteer(req contracts.AcpRunSteerRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsSteer(req)
}

func (a *App) handleAcpRunsStop(req contracts.AcpRunIDRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsStop(req)
}

func (a *App) handleAcpRunsWait(req contracts.AcpRunWaitRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsWait(req)
}

func (a *App) handleAcpRunsPromote(req contracts.AcpRunPromoteRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsPromote(req)
}

func (a *App) handleAcpRunsSetMode(req contracts.AcpRunSetModeRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandleRunsSetMode(req)
}

func (a *App) handleAcpPermissionDecide(req contracts.AcpPermissionDecideRequest) (any, *contracts.RPCError) {
	return a.subagentService().HandlePermissionDecide(req)
}

func (a *App) waitAcpRun(parent context.Context, req contracts.AcpRunWaitRequest) (*domain.AcpRun, *contracts.RPCError) {
	return a.subagentService().WaitRun(parent, req)
}

func (a *App) Subagent(ctx context.Context, argsJSON []byte) (string, error) {
	return a.subagentService().DispatchSubagent(ctx, ConversationIDFromContext(ctx), ToolCallIDFromContext(ctx), argsJSON)
}

func (a *App) EnabledAcpAgents() []*domain.AcpAgent {
	return a.subagentService().EnabledAgents()
}

func (a *App) SpawnDelegate(ctx context.Context, argsJSON []byte) (string, error) {
	return a.subagentService().SpawnDelegate(ctx, ConversationIDFromContext(ctx), ToolCallIDFromContext(ctx), argsJSON)
}

func (a *App) delegateRunSnapshot(runID string) (*domain.AcpRun, bool) {
	return a.subagentService().DelegateRunSnapshot(runID)
}

func (a *App) persistAcpRun(run *domain.AcpRun) string {
	return a.subagentService().PersistRun(run)
}

func (a *App) emitAcpRun(event string, run *domain.AcpRun) {
	a.subagentService().EmitRun(event, run)
}

func (a *App) onAcpRunDone(run *domain.AcpRun) {
	a.subagentService().OnRunDone(run)
}
