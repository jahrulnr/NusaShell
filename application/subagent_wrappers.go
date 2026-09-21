package application

import (
	"context"

	"nusashell/application/subagent"
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
//   subagentResultMessage, pendingRunDone (turn_run.go).
// Service calls DeliverRunDone / CompleteSubagent injected from App.

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

func (a *App) emitAcpRun(event string, run *domain.AcpRun) {
	a.subagentService().EmitRun(event, run)
}

func (a *App) onAcpRunDone(run *domain.AcpRun) {
	a.subagentService().OnRunDone(run)
}
