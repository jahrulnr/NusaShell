package learn

import (
	"strings"

	"nusashell/contracts"
)

// deleteLearningJob removes one learning job end-to-end: its trajectory
// events, its persisted job row, and the background conversation holding
// the LLM transcript (when the trajectory recorded one). The user asked to
// remove a learning log entry: keeping the transcript would make the
// deletion look half-done in the Learning log.
func (s *Service) DeleteLearningJob(jobID string) {
	if s == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	var transcriptIDs []string
	if s.deps.Trajectory != nil {
		transcriptIDs = s.deps.Trajectory.DeleteEvents(func(ev TrajectoryEvent) bool {
			return DetailString(ev.Detail, "job_id") == jobID
		})
	}
	if s.deps.Jobs != nil {
		_ = s.deps.Jobs.Delete(jobID)
	}
	for _, convID := range transcriptIDs {
		if !strings.HasPrefix(convID, "conv_") || !safeLearningConversationID(convID) {
			continue
		}
		if s.deps.Conversations != nil {
			_ = s.deps.Conversations.Delete(convID)
		}
	}
}

// pruneLearningEdges removes every graph edge touching a node that no
// longer exists (record deleted, skill deleted). The graph renderer also
// filters dangling edges, so this keeps edges.jsonl tidy rather than
// correct-by-render.
func (s *Service) pruneLearningEdges(nodeID string) {
	s.PruneEdges(nodeID)
}

// detailString extracts a string detail field from a trajectory event.
func DetailString(detail map[string]interface{}, key string) string {
	if detail == nil {
		return ""
	}
	v, ok := detail[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// HandleLearningLogDelete deletes one learning log entry by job id: the
// trajectory events, the job row, and the LLM transcript conversation.
func (s *Service) HandleLearningLogDelete(req contracts.LearningLogDeleteRequest) (any, *contracts.RPCError) {
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "job id is required"}
	}
	if s.deps.Jobs != nil {
		job, err := s.deps.Jobs.Get(jobID)
		if err != nil || job == nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "learning job not found"}
		}
	}
	s.DeleteLearningJob(jobID)
	if s.deps.Bus != nil {
		s.deps.Bus.Emit(contracts.EventLearningLogDeleted, map[string]any{"job_id": jobID})
	}
	return contracts.LearningLogDeleteResult{Deleted: true}, nil
}
