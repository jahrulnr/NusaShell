package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// automationRunYAML is the agent-facing run snapshot for automation_wait /
// automation_run_status. WakeAt is formatted as RFC3339 (or omitted) so it is
// not stuffed into map[string]any as *time.Time — yaml.v3 panics on that.
func automationRunYAML(run *domain.WorkflowRun, extra map[string]any) map[string]any {
	out := map[string]any{
		"run_id":         run.ID,
		"status":         run.Status,
		"summary":        run.Summary(),
		"blocked_reason": run.BlockedReason,
	}
	if run.WakeAt != nil {
		out["wake_at"] = clock.NewTime(*run.WakeAt).Format(time.RFC3339)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (t *Toolbox) executeAutomation(ctx context.Context, name string, argsJSON []byte) (string, bool, error) {
	if t.Automation == nil {
		return "", true, depMissing("automation is not configured")
	}
	a := t.Automation
	var args map[string]any
	if err := json.Unmarshal(argsJSON, &args); err != nil || args == nil {
		if err == nil {
			err = fmt.Errorf("arguments must be a JSON object")
		}
		return "", true, fmt.Errorf("invalid automation arguments: %w", err)
	}
	str := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	encode := func(v any, err error) (string, bool, error) {
		if err != nil {
			return "", true, err
		}
		return yamlBlock(v), true, nil
	}
	switch name {
	case "automation_validate":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		raw := []byte(str("yaml"))
		r, _ := a.ValidateYAML(raw)
		return encode(r, nil)
	case "automation_run":
		async, _ := args["async"].(bool)
		id := str("workflow_id")
		if id == "" {
			return "", true, fmt.Errorf("workflow_id is required (use automation op=list to see available workflows)")
		}
		var run *domain.WorkflowRun
		var err error
		if async {
			run, err = a.RunWorkflowAsync(ctx, id, "agent")
		} else {
			run, err = a.RunWorkflow(ctx, id, "agent")
		}
		return encode(run, err)
	case "automation_wait":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		timeoutMs, _ := args["timeout_ms"].(float64)
		timeout := 5 * time.Minute
		if timeoutMs > 0 {
			timeout = time.Duration(timeoutMs) * time.Millisecond
		}
		if timeout > time.Hour {
			timeout = time.Hour
		}
		run, err := a.WaitRun(ctx, str("run_id"), timeout)
		if err != nil {
			return "", true, err
		}
		return encode(automationRunYAML(run, map[string]any{
			"timed_out": !run.Status.IsTerminal(),
		}), nil)
	case "automation_run_status":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		run, err := a.Runs.Get(ctx, str("run_id"))
		if err != nil {
			return "", true, err
		}
		return encode(automationRunYAML(run, nil), nil)
	case "automation_logs":
		jobID := strings.TrimSpace(str("job_id"))
		if jobID == "" {
			return "", true, fmt.Errorf("job_id is required")
		}
		after, _ := args["after"].(float64)
		limit, _ := args["limit"].(float64)
		if limit <= 0 {
			limit = 200
		}
		if limit > 2000 {
			limit = 2000
		}
		chunks, err := a.Logs.Read(ctx, jobID, uint64(after), int(limit))
		return encode(chunks, err)
	case "automation_cancel":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		return encode(map[string]bool{"ok": true}, a.Exec.Cancel(ctx, str("run_id")))
	case "automation_steer":
		runID := str("run_id")
		text := str("text")
		if strings.TrimSpace(runID) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		if strings.TrimSpace(text) == "" {
			return "", true, fmt.Errorf("text is required")
		}
		run, err := a.Runs.Get(ctx, runID)
		if err != nil {
			return "", true, fmt.Errorf("run not found: %w", err)
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
			return "", true, fmt.Errorf("no running agent step to steer")
		}
		if t.Steerer == nil {
			return "", true, depMissing("steer is not configured")
		}
		if err := t.Steerer.SteerHeadlessTurn(convID, text); err != nil {
			return "", true, err
		}
		return encode(map[string]any{"steered": true, "conversation_id": convID}, nil)
	case "automation_list":
		list, err := a.Workflows.List(ctx)
		if err != nil {
			return "", true, err
		}
		type row struct {
			ID, Name, Availability, Reason string
			Enabled                        bool
		}
		var out []row
		for _, w := range list {
			avail, reason := a.AvailabilityOf(ctx, w)
			out = append(out, row{ID: w.ID, Name: w.Name, Enabled: w.Enabled, Availability: avail, Reason: reason})
		}
		return encode(out, nil)
	case "automation_read":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		w, err := a.Workflows.Get(ctx, workflowID)
		if err != nil {
			return "", true, err
		}
		avail, reason := a.AvailabilityOf(ctx, w)
		caps := []any{}
		for _, name := range w.ReferencedCapabilities() {
			b, _ := a.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
			caps = append(caps, b)
		}
		return encode(map[string]any{"workflow": w, "availability": avail, "reason": reason, "capabilities": caps}, nil)
	case "automation_create":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, err
		}
		if n := str("name"); n != "" {
			w.Name = n
		}
		w.Enabled = true
		if enabled, ok := args["enabled"].(bool); ok {
			w.Enabled = enabled
		}
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "automation_enable", "automation_disable":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		w, err := a.Workflows.Get(ctx, workflowID)
		if err != nil {
			return "", true, err
		}
		if name == "automation_enable" {
			err = a.Sched.EnableWorkflow(ctx, w)
		} else {
			err = a.Sched.DisableWorkflow(ctx, w)
		}
		return encode(map[string]any{"id": w.ID, "enabled": w.Enabled}, err)
	case "automation_delete":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		if a.Workflows == nil {
			return "", true, depMissing("workflow store not configured")
		}
		err := a.Workflows.Delete(ctx, workflowID)
		return encode(map[string]any{"status": "deleted", "workflow_id": workflowID}, err)
	case "automation_schedule_once":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		at, err := time.Parse(time.RFC3339, str("at"))
		if err != nil {
			return "", true, fmt.Errorf("at must be RFC3339")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, fmt.Errorf("invalid workflow: %w", err)
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		if w.Name == "" {
			w.Name = "once"
		}
		w.Enabled = true
		w.Triggers = []domain.Trigger{{ID: "t1", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}}
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "automation_schedule_every":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, fmt.Errorf("invalid workflow: %w", err)
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		cron := strings.TrimSpace(str("cron"))
		interval := strings.TrimSpace(str("interval"))
		if cron != "" && interval != "" {
			return "", true, fmt.Errorf("cron and interval are mutually exclusive")
		}
		tr := domain.Trigger{ID: "t1", Family: domain.FamilyEvery, Timezone: str("timezone")}
		if cron != "" {
			tr.Kind = domain.TriggerCron
			tr.Cron = cron
		} else {
			d, err := time.ParseDuration(interval)
			if err != nil {
				return "", true, fmt.Errorf("interval or cron is required")
			}
			tr.Kind = domain.TriggerInterval
			tr.Interval = d
		}
		w.Triggers = []domain.Trigger{tr}
		w.Enabled = true
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	default:
		return "", false, nil
	}
}
