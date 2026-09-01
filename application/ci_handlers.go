package application

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

func (a *App) handleCI(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if a.CI == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "automation is not configured"}
	}
	auto := a.CI
	switch method {
	case contracts.MethodCIValidate:
		var req contracts.CIWorkspaceRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if req.YAML != "" {
			r, _ := auto.ValidateYAML([]byte(req.YAML))
			return validationDTO(r), nil
		}
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "yaml is required"}
	case contracts.MethodCIRunsStart:
		var req contracts.CIRunStartRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if req.ID == "" {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "id is required"}
		}
		run, err := auto.RunWorkflow(ctx, req.ID, "ui")
		if err != nil && run == nil {
			return nil, rpcWorkflowRunError(err)
		}
		if run == nil {
			return nil, rpcInternal(err)
		}
		return runDTO(run), nil
	case contracts.MethodCIRunsList:
		runs, err := auto.Runs.List(ctx, RunFilter{Limit: 50})
		if err != nil {
			return nil, rpcInternal(err)
		}
		out := make([]contracts.CIRunDTO, 0, len(runs))
		for _, r := range runs {
			out = append(out, runDTO(r))
		}
		return contracts.CIRunListResult{Runs: out}, nil
	case contracts.MethodCIRunsGet:
		var req contracts.CIRunIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		run, err := auto.Runs.Get(ctx, req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		return runDTO(run), nil
	case contracts.MethodCIRunsCancel:
		var req contracts.CIRunIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := auto.Exec.Cancel(ctx, req.ID); err != nil {
			return nil, rpcInternal(err)
		}
		run, _ := auto.Runs.Get(ctx, req.ID)
		return runDTO(run), nil
	case contracts.MethodCIRunsSteer:
		var req contracts.CIRunSteerRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Text) == "" {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "steer text is required"}
		}
		run, err := auto.Runs.Get(ctx, req.ID)
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
		if err := a.SteerHeadlessTurn(convID, req.Text); err != nil {
			return nil, rpcInternal(err)
		}
		return map[string]any{"steered": true, "conversation_id": convID}, nil
	case contracts.MethodCIRunsRetry:
		var req contracts.CIRunIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		prev, err := auto.Runs.Get(ctx, req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		run := NewWorkflowRun(prev.Definition, "retry")
		run.Workspace = prev.Workspace
		if err := auto.Exec.StartRun(ctx, run); err != nil {
			return runDTO(run), nil
		}
		got, _ := auto.Runs.Get(ctx, run.ID)
		return runDTO(got), nil
	case contracts.MethodCIJobsLogs:
		var req contracts.CILogsRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		limit := req.Limit
		if limit <= 0 {
			limit = 200
		}
		chunks, err := auto.Logs.Read(ctx, req.JobID, req.After, limit)
		if err != nil {
			return nil, rpcInternal(err)
		}
		raw := make([]json.RawMessage, 0, len(chunks))
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			raw = append(raw, b)
		}
		return contracts.CILogsResult{Chunks: raw}, nil
	case contracts.MethodCIJobsGet:
		var req contracts.CILogsRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		run, err := auto.Runs.Get(ctx, req.RunID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		jr := run.JobRunByID(req.JobID)
		if jr == nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "job not found"}
		}
		return jr, nil
	case contracts.MethodCIJobsCancel:
		var req contracts.CIRunIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := auto.Exec.Cancel(ctx, req.ID); err != nil {
			return nil, rpcInternal(err)
		}
		return map[string]bool{"ok": true}, nil
	case contracts.MethodCIArtifactsList:
		return map[string]any{"artifacts": []any{}}, nil
	case contracts.MethodCICacheList:
		return map[string]any{"entries": []any{}}, nil
	case contracts.MethodCICacheClear:
		return map[string]bool{"ok": true}, nil
	case contracts.MethodCIRunnersList:
		return map[string]any{"runners": []map[string]any{{
			"id": "local", "name": "Local machine", "executor": "local", "status": "online",
			"labels": []string{"local", "linux"},
		}}}, nil
	case contracts.MethodCIList:
		list, err := auto.Workflows.List(ctx)
		if err != nil {
			return nil, rpcInternal(err)
		}
		out := make([]contracts.CIWorkflowDTO, 0, len(list))
		for _, w := range list {
			avail, reason := auto.AvailabilityOf(ctx, w)
			out = append(out, workflowDTO(w, avail, reason))
		}
		return contracts.CIWorkflowListResult{Workflows: out}, nil
	case contracts.MethodCIGet:
		var req contracts.CIWorkflowIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		w, err := auto.Workflows.Get(ctx, req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		avail, reason := auto.AvailabilityOf(ctx, w)
		return workflowDTO(w, avail, reason), nil
	case contracts.MethodCISave:
		var req contracts.CIWorkflowSaveRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		w, err := auto.ParseDefinition(req.YAML)
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
		saved, r, err := auto.SaveWorkflow(ctx, w)
		if err != nil && r.Verdict() == "INVALID" {
			return validationDTO(r), &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
		}
		if err != nil {
			return nil, rpcInternal(err)
		}
		avail, reason := auto.AvailabilityOf(ctx, saved)
		return map[string]any{"workflow": workflowDTO(saved, avail, reason), "validation": validationDTO(r)}, nil
	case contracts.MethodCIDelete:
		var req contracts.CIWorkflowIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := auto.Workflows.Delete(ctx, req.ID); err != nil {
			return nil, rpcInternal(err)
		}
		return map[string]bool{"ok": true}, nil
	case contracts.MethodCIEnable, contracts.MethodCIDisable:
		var req contracts.CIWorkflowIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		w, err := auto.Workflows.Get(ctx, req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		if method == contracts.MethodCIEnable {
			if err := auto.Sched.EnableWorkflow(ctx, w); err != nil {
				return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
			}
		} else {
			w.Enabled = false
			_ = auto.Workflows.Put(ctx, w)
		}
		avail, reason := auto.AvailabilityOf(ctx, w)
		return workflowDTO(w, avail, reason), nil
	case contracts.MethodCIRun:
		var req contracts.CIWorkflowIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		run, err := auto.RunWorkflow(ctx, req.ID, "ui")
		if err != nil && run == nil {
			return nil, rpcWorkflowRunError(err)
		}
		return runDTO(run), nil
	case contracts.MethodCIEvents:
		evs, err := auto.Events.ListEvents(ctx, 50)
		if err != nil {
			return nil, rpcInternal(err)
		}
		out := make([]contracts.EventDTO, 0, len(evs))
		for _, e := range evs {
			out = append(out, contracts.EventDTO{
				ID: e.ID, Type: e.Type, Source: e.Source, Subject: e.Subject,
				Time: clock.NewTime(e.Time).Format(time.RFC3339), Attrs: e.Attributes,
			})
		}
		return map[string]any{"events": out}, nil
	case contracts.MethodCIIngest:
		var req contracts.CIIngestRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		ev := domain.Event{ID: req.ID, Type: req.Type, Source: req.Source, Subject: req.Subject, Attributes: req.Attributes, Time: clock.NewTime().Time()}
		if err := auto.Sched.IngestEvent(ctx, ev); err != nil {
			return nil, rpcInternal(err)
		}
		return map[string]bool{"ok": true}, nil
	case contracts.MethodCISchedules:
		list, err := auto.Schedules.List(ctx)
		if err != nil {
			return nil, rpcInternal(err)
		}
		out := make([]contracts.ScheduleDTO, 0, len(list))
		for _, rec := range list {
			out = append(out, contracts.ScheduleDTO{
				ID: rec.ID, WorkflowID: rec.WorkflowID, Kind: string(rec.Kind),
				NextRunAt: clock.NewTime(rec.NextRunAt).Format(time.RFC3339), Status: string(rec.Status), Timezone: rec.Timezone,
			})
		}
		return map[string]any{"schedules": out}, nil
	case contracts.MethodCICapabilities:
		list := auto.Caps.List(ctx)
		out := make([]contracts.CapabilityDTO, 0, len(list))
		for _, b := range list {
			out = append(out, contracts.CapabilityDTO{
				Capability: b.Capability, Provider: b.ProviderID, Kind: string(b.Kind), Status: string(b.Status), Reason: b.Reason,
			})
		}
		return map[string]any{"capabilities": out}, nil
	case contracts.MethodCIDependents:
		var req contracts.PluginIDRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		deps, err := auto.Caps.Dependents(ctx, strings.TrimPrefix(req.ID, "plugin:"))
		if err != nil {
			return nil, rpcInternal(err)
		}
		names := make([]string, 0, len(deps))
		for _, d := range deps {
			names = append(names, d.Name)
		}
		return map[string]any{"automations": names, "count": len(names)}, nil
	case contracts.MethodCIProviderDisable:
		var req contracts.PluginSetFlagRequest
		if err := contracts.DecodePayload(payload, &req); err != nil {
			return nil, err
		}
		if err := auto.Caps.SetDisabled(ctx, req.ID, !req.Enabled); err != nil {
			return nil, rpcInternal(err)
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "unknown ci method"}
	}
}

func rpcWorkflowRunError(err error) *contracts.RPCError {
	if strings.Contains(err.Error(), "invalid workflow") {
		return &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	return rpcInternal(err)
}
