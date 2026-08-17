package domain

import (
	"fmt"
	"strings"
	"time"
)

// ValidateSyntax checks structural workflow rules. It does not resolve
// capabilities or provider availability.
func ValidateSyntax(w *WorkflowDefinition) ValidationResult {
	r := NewValidationResult()
	if w == nil {
		r.Add(ValidationIssue{Path: "", Code: "empty", Message: "workflow is empty", Level: ValidationSyntax})
		return r
	}
	if strings.TrimSpace(w.Name) == "" {
		r.Add(ValidationIssue{Path: "name", Code: "missing_name", Message: "name is required", Level: ValidationSyntax})
	}
	if w.Version != 0 && w.Version != 1 {
		r.Add(ValidationIssue{Path: "version", Code: "unsupported_version", Message: fmt.Sprintf("unsupported version %d", w.Version), Level: ValidationSyntax})
	}
	if w.Trust != "" && w.Trust != TrustSafe && w.Trust != TrustTrusted && w.Trust != TrustPrivileged {
		r.Add(ValidationIssue{Path: "trust", Code: "invalid_trust", Message: "trust must be safe, trusted, or privileged", Level: ValidationSyntax})
	}
	c := w.Concurrency.Normalized()
	switch c.Policy {
	case ConcurrencyAllow, ConcurrencyQueue, ConcurrencyReplace, ConcurrencySkip:
	default:
		r.Add(ValidationIssue{Path: "concurrency.policy", Code: "invalid_concurrency", Message: "policy must be allow, queue, replace, or skip", Level: ValidationSyntax})
	}
	switch w.Missed {
	case "", MissedSkip, MissedRunOnce, MissedCatchUpAll:
	default:
		r.Add(ValidationIssue{Path: "missed", Code: "invalid_missed", Message: "unknown missed-run policy", Level: ValidationSyntax})
	}
	if len(w.Jobs) == 0 {
		r.Add(ValidationIssue{Path: "jobs", Code: "no_jobs", Message: "at least one job is required", Level: ValidationSyntax})
	}
	for i, t := range w.Triggers {
		validateTrigger(&r, i, t)
	}
	for i, j := range w.Jobs {
		validateJob(&r, i, j)
	}
	if len(w.Jobs) > 0 {
		_, dagIssues := BuildDAG(w.Jobs)
		for _, issue := range dagIssues {
			r.Add(issue)
		}
	}
	return r
}

func validateTrigger(r *ValidationResult, i int, t Trigger) {
	path := fmt.Sprintf("triggers[%d]", i)
	switch t.Kind {
	case TriggerOnce:
		if t.At == nil {
			r.Add(ValidationIssue{Path: path + ".once.at", Code: "missing_at", Message: "once trigger requires at", Level: ValidationSyntax})
		}
		if t.Timezone != "" {
			if _, err := time.LoadLocation(t.Timezone); err != nil {
				r.Add(ValidationIssue{Path: path + ".timezone", Code: "invalid_timezone", Message: "unknown IANA timezone", Level: ValidationSyntax})
			}
		}
	case TriggerInterval:
		if t.Interval <= 0 {
			r.Add(ValidationIssue{Path: path + ".every.interval", Code: "invalid_interval", Message: "interval must be positive", Level: ValidationSyntax})
		}
	case TriggerCron:
		if t.Cron == "" {
			r.Add(ValidationIssue{Path: path + ".every.cron", Code: "missing_cron", Message: "cron expression is required", Level: ValidationSyntax})
		} else if _, err := ParseCron(t.Cron); err != nil {
			r.Add(ValidationIssue{Path: path + ".every.cron", Code: "invalid_cron", Message: err.Error(), Level: ValidationSyntax})
		}
		if t.Timezone != "" {
			if _, err := time.LoadLocation(t.Timezone); err != nil {
				r.Add(ValidationIssue{Path: path + ".timezone", Code: "invalid_timezone", Message: "unknown IANA timezone", Level: ValidationSyntax})
			}
		}
	case TriggerEvent:
		if strings.TrimSpace(t.Event) == "" {
			r.Add(ValidationIssue{Path: path + ".when.event", Code: "missing_event", Message: "when trigger requires event", Level: ValidationSyntax})
		}
	case TriggerManual:
		// ok
	case "":
		r.Add(ValidationIssue{Path: path, Code: "missing_kind", Message: "trigger kind is required", Level: ValidationSyntax})
	default:
		r.Add(ValidationIssue{Path: path, Code: "unknown_kind", Message: "unknown trigger kind", Level: ValidationSyntax})
	}
}

func validateJob(r *ValidationResult, i int, j Job) {
	path := fmt.Sprintf("jobs[%d]", i)
	if j.ID == "" {
		r.Add(ValidationIssue{Path: path + ".id", Code: "missing_job_id", Message: "job id is required", Level: ValidationSyntax})
	} else {
		path = "jobs." + j.ID
	}
	if j.Timeout < 0 {
		r.Add(ValidationIssue{Path: path + ".timeout", Code: "invalid_timeout", Message: "timeout must be non-negative", Level: ValidationSyntax})
	}
	if j.Retry.MaxAttempts < 0 {
		r.Add(ValidationIssue{Path: path + ".retry.max_attempts", Code: "invalid_retry", Message: "max_attempts must be non-negative", Level: ValidationSyntax})
	}
	for _, p := range j.Artifacts.Paths {
		if strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			r.Add(ValidationIssue{Path: path + ".artifacts.paths", Code: "invalid_artifact_path", Message: "artifact paths must be relative and stay inside the workspace", Level: ValidationSyntax})
		}
	}
	if len(j.Steps) == 0 {
		r.Add(ValidationIssue{Path: path + ".steps", Code: "no_steps", Message: "job has no steps", Level: ValidationSyntax})
	}
	for si, s := range j.Steps {
		n := 0
		if s.Run != "" {
			n++
		}
		if s.Uses != "" {
			n++
		}
		if s.WaitUntil != nil {
			n++
		}
		if s.Agent != nil && s.Agent.Prompt != "" {
			n++
		}
		if n == 0 {
			r.Add(ValidationIssue{Path: fmt.Sprintf("%s.steps[%d]", path, si), Code: "empty_step", Message: "step requires run, uses, wait_until, or agent", Level: ValidationSyntax})
		}
		if n > 1 {
			r.Add(ValidationIssue{Path: fmt.Sprintf("%s.steps[%d]", path, si), Code: "ambiguous_step", Message: "step may specify only one of run, uses, wait_until, or agent", Level: ValidationSyntax})
		}
		if s.Timeout < 0 {
			r.Add(ValidationIssue{Path: fmt.Sprintf("%s.steps[%d].timeout", path, si), Code: "invalid_timeout", Message: "timeout must be non-negative", Level: ValidationSyntax})
		}
		if s.Shell != "" {
			switch s.Shell {
			case "auto", "sh", "bash", "pwsh", "powershell":
			default:
				r.Add(ValidationIssue{Path: fmt.Sprintf("%s.steps[%d].shell", path, si), Code: "unsupported_shell", Message: "unsupported shell", Level: ValidationSyntax})
			}
		}
	}
}
