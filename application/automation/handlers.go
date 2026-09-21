package automation

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
	clock "nusashell/pkg/time"
)

func (a *Automation) handleValidate(req contracts.AutomationWorkspaceRequest) (any, *contracts.RPCError) {
	if req.YAML != "" {
		r, _ := a.ValidateYAML([]byte(req.YAML))
		return validationDTO(r), nil
	}
	return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "yaml is required"}
}

func (a *Automation) handleRunsStart(ctx context.Context, req contracts.AutomationRunStartRequest) (any, *contracts.RPCError) {
	if req.ID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id is required"}
	}
	run, err := a.RunWorkflow(ctx, req.ID, "ui")
	if err != nil && run == nil {
		return nil, rpcWorkflowRunError(err)
	}
	if run == nil {
		return nil, rpcdispatch.Internal(err)
	}
	return runDTO(run), nil
}

func (a *Automation) handleRunsList(ctx context.Context) (any, *contracts.RPCError) {
	runs, err := a.Runs.List(ctx, RunFilter{Limit: 50})
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	out := make([]contracts.RunDTO, 0, len(runs))
	for _, r := range runs {
		out = append(out, runDTO(r))
	}
	return contracts.RunListResult{Runs: out}, nil
}

func (a *Automation) handleRunsGet(ctx context.Context, req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
	run, err := a.Runs.Get(ctx, req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return runDTO(run), nil
}

func (a *Automation) handleRunsCancel(ctx context.Context, req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
	if err := a.Exec.Cancel(ctx, req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	run, _ := a.Runs.Get(ctx, req.ID)
	return runDTO(run), nil
}

func (a *Automation) handleRunsSteer(ctx context.Context, steer HeadlessTurnRunner, req contracts.AutomationRunSteerRequest) (any, *contracts.RPCError) {
	if strings.TrimSpace(req.Text) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "steer text is required"}
	}
	run, err := a.Runs.Get(ctx, req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	var convID string
	for _, j := range run.Jobs {
		for _, s := range j.Steps {
			if s.Status == domain.StatusRunning && s.ConversationID != "" {
				convID = s.ConversationID
			}
		}
	}
	if convID == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeConflict, Message: "no running agent step to steer"}
	}
	if steer == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeInternal, Message: "headless turn runner not configured"}
	}
	if err := steer.SteerHeadlessTurn(convID, req.Text); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return map[string]any{"steered": true, "conversation_id": convID}, nil
}

func (a *Automation) handleRunsRetry(ctx context.Context, req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
	prev, err := a.Runs.Get(ctx, req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	run := NewWorkflowRun(prev.Definition, "retry")
	run.Workspace = prev.Workspace
	if err := a.Exec.StartRun(ctx, run); err != nil {
		return runDTO(run), nil
	}
	got, _ := a.Runs.Get(ctx, run.ID)
	return runDTO(got), nil
}

func (a *Automation) handleJobsLogs(ctx context.Context, req contracts.AutomationLogsRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	chunks, err := a.Logs.Read(ctx, req.JobID, req.After, limit)
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	raw := make([]json.RawMessage, 0, len(chunks))
	for _, c := range chunks {
		b, _ := json.Marshal(c)
		raw = append(raw, b)
	}
	return contracts.AutomationLogsResult{Chunks: raw}, nil
}

func (a *Automation) handleJobsGet(ctx context.Context, req contracts.AutomationLogsRequest) (any, *contracts.RPCError) {
	run, err := a.Runs.Get(ctx, req.RunID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	jr := run.JobRunByID(req.JobID)
	if jr == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "job not found"}
	}
	return jr, nil
}

func (a *Automation) handleJobsCancel(ctx context.Context, req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
	if err := a.Exec.Cancel(ctx, req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (a *Automation) handleArtifactsList() (any, *contracts.RPCError) {
	return map[string]any{"artifacts": []any{}}, nil
}

func (a *Automation) handleCacheList() (any, *contracts.RPCError) {
	return map[string]any{"entries": []any{}}, nil
}

func (a *Automation) handleCacheClear() (any, *contracts.RPCError) {
	return map[string]bool{"ok": true}, nil
}

func (a *Automation) handleRunnersList() (any, *contracts.RPCError) {
	return map[string]any{"runners": []map[string]any{{
		"id": "local", "name": "Local machine", "executor": "local", "status": "online",
		"labels": []string{"local", "linux"},
	}}}, nil
}

func (a *Automation) handleList(ctx context.Context) (any, *contracts.RPCError) {
	list, err := a.Workflows.List(ctx)
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	out := make([]contracts.WorkflowDTO, 0, len(list))
	for _, w := range list {
		avail, reason := a.AvailabilityOf(ctx, w)
		out = append(out, workflowDTO(w, avail, reason))
	}
	return contracts.WorkflowListResult{Workflows: out}, nil
}

func (a *Automation) handleGet(ctx context.Context, req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
	w, err := a.Workflows.Get(ctx, req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	avail, reason := a.AvailabilityOf(ctx, w)
	return workflowDTO(w, avail, reason), nil
}

func (a *Automation) handleSave(ctx context.Context, req contracts.AutomationWorkflowSaveRequest) (any, *contracts.RPCError) {
	w, err := a.ParseDefinition(req.YAML)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if req.ID != "" {
		w.ID = req.ID
	}
	if req.Name != "" {
		w.Name = req.Name
	}
	if req.Enabled != nil {
		w.Enabled = *req.Enabled
	} else {
		w.Enabled = true
	}
	saved, r, err := a.SaveWorkflow(ctx, w)
	if err != nil && r.Verdict() == "INVALID" {
		return validationDTO(r), &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	avail, reason := a.AvailabilityOf(ctx, saved)
	return map[string]any{"workflow": workflowDTO(saved, avail, reason), "validation": validationDTO(r)}, nil
}

func (a *Automation) handleDelete(ctx context.Context, req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
	if err := a.Workflows.Delete(ctx, req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (a *Automation) handleSetEnabled(ctx context.Context, req contracts.AutomationWorkflowIDRequest, enable bool) (any, *contracts.RPCError) {
	w, err := a.Workflows.Get(ctx, req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if enable {
		if err := a.Sched.EnableWorkflow(ctx, w); err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
		}
	} else {
		if err := a.Sched.DisableWorkflow(ctx, w); err != nil {
			return nil, rpcdispatch.Internal(err)
		}
	}
	avail, reason := a.AvailabilityOf(ctx, w)
	return workflowDTO(w, avail, reason), nil
}

func (a *Automation) handleRun(ctx context.Context, req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
	run, err := a.RunWorkflow(ctx, req.ID, "ui")
	if err != nil && run == nil {
		return nil, rpcWorkflowRunError(err)
	}
	return runDTO(run), nil
}

func (a *Automation) handleEvents(ctx context.Context) (any, *contracts.RPCError) {
	evs, err := a.Events.ListEvents(ctx, 50)
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	out := make([]contracts.EventDTO, 0, len(evs))
	for _, e := range evs {
		out = append(out, contracts.EventDTO{
			ID: e.ID, Type: e.Type, Source: e.Source, Subject: e.Subject,
			Time: clock.NewTime(e.Time).Format(time.RFC3339), Attrs: e.Attributes,
		})
	}
	return map[string]any{"events": out}, nil
}

func (a *Automation) handleIngest(ctx context.Context, req contracts.AutomationIngestRequest) (any, *contracts.RPCError) {
	ev := domain.Event{ID: req.ID, Type: req.Type, Source: req.Source, Subject: req.Subject, Attributes: req.Attributes, Time: clock.NewTime().Time()}
	if err := a.Sched.IngestEvent(ctx, ev); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func (a *Automation) handleSchedules(ctx context.Context) (any, *contracts.RPCError) {
	list, err := a.Schedules.List(ctx)
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	out := make([]contracts.ScheduleDTO, 0, len(list))
	for _, rec := range list {
		out = append(out, contracts.ScheduleDTO{
			ID: rec.ID, WorkflowID: rec.WorkflowID, Kind: string(rec.Kind),
			NextRunAt: clock.NewTime(rec.NextRunAt).Format(time.RFC3339), Status: string(rec.Status), Timezone: rec.Timezone,
		})
	}
	return map[string]any{"schedules": out}, nil
}

func (a *Automation) handleCapabilities(ctx context.Context) (any, *contracts.RPCError) {
	list := a.Caps.List(ctx)
	out := make([]contracts.CapabilityDTO, 0, len(list))
	for _, b := range list {
		out = append(out, contracts.CapabilityDTO{
			Capability: b.Capability, Provider: b.ProviderID, Kind: string(b.Kind), Status: string(b.Status), Reason: b.Reason,
		})
	}
	return map[string]any{"capabilities": out}, nil
}

func (a *Automation) handleDependents(ctx context.Context, req contracts.PluginIDRequest) (any, *contracts.RPCError) {
	deps, err := a.Caps.Dependents(ctx, strings.TrimPrefix(req.ID, "plugin:"))
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	names := make([]string, 0, len(deps))
	for _, d := range deps {
		names = append(names, d.Name)
	}
	return map[string]any{"automations": names, "count": len(names)}, nil
}

func (a *Automation) handleProviderDisable(ctx context.Context, req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
	if err := a.Caps.SetDisabled(ctx, req.ID, !req.Enabled); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return map[string]bool{"ok": true}, nil
}

func rpcWorkflowRunError(err error) *contracts.RPCError {
	if strings.Contains(err.Error(), "invalid workflow") {
		return &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	return rpcdispatch.Internal(err)
}
