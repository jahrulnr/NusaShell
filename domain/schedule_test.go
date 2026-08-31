package domain

import (
	"testing"
	"time"

	clock "nusashell/pkg/time"
)

func TestTriggerLocationDefaultsToMachineTime(t *testing.T) {
	want := clock.NewTime().Time().Location()
	for _, trigger := range []Trigger{{}, {Timezone: "not/a-real-timezone"}} {
		if got := trigger.Location(); got != want {
			t.Fatalf("trigger location = %v, want machine location %v", got, want)
		}
	}
}

func TestNextFireOnceFuture(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, loc)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, loc)
	got, err := NextFire(Trigger{Kind: TriggerOnce, At: &at, Timezone: "Asia/Jakarta"}, now, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(at) {
		t.Fatalf("got %v want %v", got, at)
	}
}

func TestNextFireOnceMissedRunOnceAfterRestart(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, loc)
	now := time.Date(2026, 8, 18, 13, 0, 0, 0, loc)
	got, err := NextFire(Trigger{Kind: TriggerOnce, At: &at, Timezone: "Asia/Jakarta"}, now, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Equal(at) {
		t.Fatalf("once default is run_once_after_restart, got %v", got)
	}
}

func TestNextFireCronSkipMissed(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 13, 0, 0, 0, loc)
	got, err := NextFire(Trigger{Kind: TriggerCron, Cron: "0 12 * * *", Timezone: "Asia/Jakarta"}, now, nil, MissedSkip)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nil")
	}
	want := time.Date(2026, 8, 19, 12, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCronDoesNotEqualInterval(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, loc)
	cronNext, err := NextFire(Trigger{Kind: TriggerCron, Cron: "0 12 * * *", Timezone: "UTC"}, now, nil, MissedSkip)
	if err != nil {
		t.Fatal(err)
	}
	intervalNext, err := NextFire(Trigger{Kind: TriggerInterval, Interval: 24 * time.Hour}, now, &now, MissedSkip)
	if err != nil {
		t.Fatal(err)
	}
	// Interval is elapsed-time from last; cron is calendar 12:00 next day.
	if !cronNext.Equal(time.Date(2026, 8, 19, 12, 0, 0, 0, loc)) {
		t.Fatalf("cron next = %v", cronNext)
	}
	if !intervalNext.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("interval next = %v", intervalNext)
	}
}

func TestParseCronInvalid(t *testing.T) {
	if _, err := ParseCron("0 12 *"); err == nil {
		t.Fatal("expected error")
	}
}

func TestEventMatchWhere(t *testing.T) {
	e := Event{
		Type:    "email.received",
		Subject: "Invoice #42",
		Attributes: map[string]any{
			"mailbox": "finance",
			"from":    "alerts@example.com",
		},
	}
	if !e.Match("email.received", map[string]any{"mailbox": "finance", "subject_contains": "invoice"}) {
		t.Fatal("should match")
	}
	if e.Match("email.received", map[string]any{"mailbox": "work"}) {
		t.Fatal("mailbox mismatch")
	}
	if e.Match("git.push", nil) {
		t.Fatal("type mismatch")
	}
}

func TestEvalIf(t *testing.T) {
	env := ConditionEnv{
		Event: Event{Subject: "urgent: down"},
		Jobs: map[string]JobRun{
			"check":  {TaskState: TaskState[RunStatus]{Status: StatusSuccess}, Outputs: map[string]any{"status": "unhealthy"}},
			"decide": {TaskState: TaskState[RunStatus]{Status: StatusSuccess}, Outputs: map[string]any{"decision": "approve"}},
		},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"", true},
		{"true", true},
		{"false", false},
		{`check.status == "unhealthy"`, true},
		{`check.run_status == "success"`, true},
		{`decide.decision == "approve"`, true},
		{`event.subject_contains("urgent")`, true},
		{`check.status == "unhealthy" && decide.decision == "ignore"`, false},
	}
	for _, c := range cases {
		got, err := EvalIf(c.expr, env)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestMapAvailability(t *testing.T) {
	if MapAvailability(CapAvailable, false) != AvailRunnable {
		t.Fatal("available")
	}
	if MapAvailability(CapNotRunning, true) != AvailPendingProvider {
		t.Fatal("not_running + auto-start")
	}
	if MapAvailability(CapNotRunning, false) != AvailBlocked {
		t.Fatal("not_running without auto-start")
	}
	if MapAvailability(CapDisabled, true) != AvailBlocked {
		t.Fatal("disabled never auto-starts")
	}
	if MapAvailability(CapMissing, true) != AvailBlocked {
		t.Fatal("missing is blocked")
	}
}

func TestAllowsAutoStartDisabledWins(t *testing.T) {
	if AllowsAutoStart(CapDisabled, AutoStartAllow, true) {
		t.Fatal("disabled_by_user must not auto-start")
	}
	if !AllowsAutoStart(CapNotRunning, AutoStartAllow, false) {
		t.Fatal("not_running can auto-start")
	}
	if AllowsAutoStart(CapNotRunning, AutoStartAlwaysRequireActive, true) {
		t.Fatal("always_require_active")
	}
}

func TestValidationResultDoesNotConflate(t *testing.T) {
	r := NewValidationResult()
	r.Add(ValidationIssue{Level: ValidationProviders, Code: "provider_disabled", Message: "mail-mcp disabled", Path: "providers"})
	r.ProviderID = "mail-mcp"
	if r.Verdict() != "BLOCKED" {
		t.Fatalf("verdict = %s", r.Verdict())
	}
	if r.Syntax != "OK" || r.Capabilities != "OK" {
		t.Fatalf("syntax/capabilities should stay OK: %+v", r)
	}
	inv := NewValidationResult()
	inv.Add(ValidationIssue{Level: ValidationCapabilities, Code: "unknown_capability", Message: "email.send does not exist", Path: "jobs.a.uses"})
	if inv.Verdict() != "INVALID" {
		t.Fatalf("missing capability is INVALID, got %s", inv.Verdict())
	}
}

func TestRunSummary(t *testing.T) {
	run := WorkflowRun{Jobs: []JobRun{
		{TaskState: TaskState[RunStatus]{Status: StatusSuccess}},
		{TaskState: TaskState[RunStatus]{Status: StatusFailed}},
		{TaskState: TaskState[RunStatus]{Status: StatusWaiting}},
		{TaskState: TaskState[RunStatus]{Status: StatusBlocked}},
	}}
	s := run.Summary()
	if s.Success != 1 || s.Failed != 1 || s.Waiting != 1 || s.Blocked != 1 {
		t.Fatalf("%+v", s)
	}
}
