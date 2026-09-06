package application

import (
	"strings"

	"nusashell/contracts"
)

// deleteLearningJob removes one learning job end-to-end: its trajectory
// events, its persisted job row, and the background conversation holding
// the LLM transcript (when the trajectory recorded one). The user asked to
// remove a learning log entry: keeping the transcript would make the
// deletion look half-done in the Learning log.
func (a *App) deleteLearningJob(jobID string) {
	if a == nil || strings.TrimSpace(jobID) == "" {
		return
	}
	var transcriptIDs []string
	if a.Trajectory != nil {
		transcriptIDs = a.Trajectory.DeleteEvents(func(ev TrajectoryEvent) bool {
			return detailString(ev.Detail, "job_id") == jobID
		})
	}
	if a.LearningJobs != nil {
		_ = a.LearningJobs.Delete(jobID)
	}
	for _, convID := range transcriptIDs {
		if !strings.HasPrefix(convID, "conv_") || !safeLearningConversationID(convID) {
			continue
		}
		if a.Conversations != nil {
			_ = a.Conversations.Delete(convID)
		}
	}
}

// pruneLearningEdges removes every graph edge touching a node that no
// longer exists (record deleted, skill deleted). The graph renderer also
// filters dangling edges, so this keeps edges.jsonl tidy rather than
// correct-by-render.
func (a *App) pruneLearningEdges(nodeID string) {
	gs := a.graph()
	if gs == nil || strings.TrimSpace(nodeID) == "" {
		return
	}
	for _, e := range gs.AllEdges() {
		if e == nil || e.InvalidAt != nil {
			continue
		}
		if e.SourceID == nodeID || e.TargetID == nodeID {
			_ = gs.DeleteEdge(e.ID)
		}
	}
}

// detailString extracts a string detail field from a trajectory event.
func detailString(detail map[string]interface{}, key string) string {
	if detail == nil {
		return ""
	}
	v, ok := detail[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// handleLearningLogDelete deletes one learning log entry by job id: the
// trajectory events, the job row, and the LLM transcript conversation.
func (a *App) handleLearningLogDelete(req contracts.LearningLogDeleteRequest) (any, *contracts.RPCError) {
	jobID := strings.TrimSpace(req.JobID)
	if jobID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "job id is required"}
	}
	if a.LearningJobs != nil {
		job, err := a.LearningJobs.Get(jobID)
		if err != nil || job == nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "learning job not found"}
		}
	}
	a.deleteLearningJob(jobID)
	if a.Bus != nil {
		a.Bus.Emit(contracts.EventLearningLogDeleted, map[string]any{"job_id": jobID})
	}
	return contracts.LearningLogDeleteResult{Deleted: true}, nil
}
