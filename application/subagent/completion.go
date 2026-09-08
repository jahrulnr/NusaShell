package subagent

import (
	"nusashell/contracts"
	"nusashell/domain"
)

// EmitRun publishes an ACP run lifecycle event.
func (s *Service) EmitRun(event string, run *domain.AcpRun) {
	if s == nil || run == nil || s.deps.Bus == nil {
		return
	}
	s.deps.Bus.Emit(event, contracts.AcpRunEvent{Run: runDTO(run)})
}

// OnRunDone persists the transcript then delivers the result through the
// injected DeliverRunDone path (queued at the next tool-round boundary on a
// live parent turn, or injected immediately plus a new turn when idle).
func (s *Service) OnRunDone(run *domain.AcpRun) {
	if run == nil || run.ConversationID == "" {
		return
	}

	outputPath := s.PersistRun(run)
	status := domain.ToolOK
	if run.Status == domain.AcpRunFailed || run.Status == domain.AcpRunCancelled {
		status = domain.ToolFailed
	}
	if s.deps.DeliverRunDone == nil {
		return
	}
	s.deps.DeliverRunDone(run.ConversationID, run.ID, func(cid string) error {
		if s.deps.CompleteSubagent == nil {
			return nil
		}
		return s.deps.CompleteSubagent(cid, run.ParentToolCallID, status, run, outputPath)
	})
}

// PersistRun writes a settled ACP run to storage. Live runs are skipped.
func (s *Service) PersistRun(run *domain.AcpRun) string {
	if s == nil || run == nil || run.Live() || s.deps.RunStorage == nil {
		return ""
	}
	// Race guard: if the conversation was deleted between OnDone being
	// scheduled and the deferred completion callback firing, skip the
	// write so a late completion cannot recreate conversations/<id>.acp/
	// sidecars the cascade just removed.
	if s.deps.Conversations != nil {
		if _, err := s.deps.Conversations.Get(run.ConversationID); err != nil {
			return ""
		}
	}
	record := domain.AcpRunRecord{
		ID:               run.ID,
		AgentID:          run.AgentID,
		AgentName:        run.AgentName,
		Title:            run.Title,
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
		EndedAt:          run.FinishedAt,
	}
	if err := s.deps.RunStorage.Save(record); err != nil {
		s.log("error", "acp", "failed to persist run %s: %v", run.ID, err)
		return ""
	}
	return s.deps.RunStorage.Path(run.ConversationID, run.ID)
}
